package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/trade-tariff/trade-tariff-lambdas-fpo-model-garbage-collection/logger"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	bucket    = "trade-tariff-models-382373577178"
	repoUrl   = "https://github.com/trade-tariff/trade-tariff-lambdas-fpo-search"
	clonePath = "/tmp/trade-tariff-lambdas-fpo-search"
)

type Model struct {
	Version                string
	ShortCommit            string
	Keys                   []string
	Deployed               bool
	UnderActiveDevelopment bool
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_VERSION") != "" {
		logger.Log.Info("Running in AWS Lambda environment")
		lambda.Start(execute)
	} else {
		logger.Log.Info("Running in local environment")
		execute()
	}
}

func execute() {
	client := s3.New(initializeAWSSession())
	repo := fetchRepo()
	relevantBranches := fetchRemoteBranches(*repo)
	relevantCommits := fetchRemoteCommits(*repo, relevantBranches)
	relevantModels := fetchS3ModelVersions(client, relevantCommits)

	if dryRun() {
		prettyPrint(relevantModels)
	} else {
		deleteModelVersions(client, relevantModels)
	}
}

func deleteModelVersions(client *s3.S3, models map[string]Model) {
	for _, model := range models {
		for _, key := range model.Keys {
			_, err := client.DeleteObject(&s3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
			})
			checkIfError(err)
		}
	}
}

// We want a map of versions to keys like so:
//
//	{
//		"1.0.1-2112d82": Model{
//			Version: "1.0.1",
//			Commit: "2112d82",
//			Keys: ["1.0.1-2112d82/...", "1.0.1-2112d82/..."]
//			Deployed: true,
//			UnderActiveDevelopment: false,
//	}
//
// We'll then make choices on which versions to preserve based on whether:
// 1. They have been deployed to production and staging
// 2. They are under active development in a branch
func fetchS3ModelVersions(client *s3.S3, outstandingCommits []*object.Commit) map[string]Model {
	models := make(map[string]Model)
	pattern := `^(\d+\.\d+\.\d+)-([a-f0-9]{7})/.*$`
	bucket := "trade-tariff-models-382373577178"

	resp, err := client.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	checkIfError(err)

	re := regexp.MustCompile(pattern)

	for _, obj := range resp.Contents {
		key := *obj.Key
		matches := re.FindStringSubmatch(key)

		if len(matches) == 3 {
			version := matches[1]
			commit := matches[2]
			version_key := version + "-" + commit

			model, exists := models[version_key]
			if !exists {
				model = Model{
					Version:                version,
					ShortCommit:            commit,
					Keys:                   make([]string, 0),
					Deployed:               false,
					UnderActiveDevelopment: false,
				}
			}
			model.Keys = append(model.Keys, key)

			if strings.Contains(key, "production") || strings.Contains(key, "staging") {
				model.Deployed = true
			}
			models[version_key] = model
		}
	}

	for _, commit := range outstandingCommits {
		for key, model := range models {
			short_commit_sha := commit.Hash.String()[0:7]
			if short_commit_sha == model.ShortCommit {
				model.UnderActiveDevelopment = true
				models[key] = model
			}
		}
	}

	relevant_models := make(map[string]Model)
	for key, model := range models {
		if !model.Deployed && !model.UnderActiveDevelopment {
			relevant_models[key] = model
		}
	}

	return relevant_models
}

func initializeAWSSession() *session.Session {
	sess, err := session.NewSession(&aws.Config{})
	checkIfError(err)
	return sess
}

func fetchRemoteCommits(r git.Repository, branch_refs []*plumbing.Reference) []*object.Commit {
	allCommits := []*object.Commit{}
	mainBranch, err := r.Reference("refs/remotes/origin/main", true)
	checkIfError(err)

	for _, targetBranch := range branch_refs {
		targetCommit, err := r.CommitObject(targetBranch.Hash())
		checkIfError(err)
		mainCommit, err := r.CommitObject(mainBranch.Hash())
		checkIfError(err)
		mergeBase, err := targetCommit.MergeBase(mainCommit)
		checkIfError(err)

		if len(mergeBase) == 0 {
			logger.Log.Fatal("No merge base found", logger.String("targetBranch", targetBranch.Name().Short()))
		}

		commitIter, err := r.Log(&git.LogOptions{
			From: targetBranch.Hash(),
		})
		checkIfError(err)

		err = commitIter.ForEach(func(c *object.Commit) error {
			if c == nil {
				return nil
			}
			if c.Hash == mergeBase[0].Hash {
				return storer.ErrStop
			}
			allCommits = append(allCommits, c)
			return nil
		})

		checkIfError(err)
	}
	return allCommits
}

func fetchRemoteBranches(r git.Repository) []*plumbing.Reference {
	relevantBranches := []*plumbing.Reference{}

	remoteBranches, err := r.References()
	checkIfError(err)

	err = remoteBranches.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsRemote() {
			name := ref.Name().Short()
			if name == "HEAD" {
				return nil
			}
			if name == "origin/main" {
				return nil
			}
			if strings.Contains(name, "dependabot") {
				return nil
			}

			relevantBranches = append(relevantBranches, ref)
		}
		return nil
	})
	checkIfError(err)

	return relevantBranches
}

func fetchRepo() *git.Repository {
	r, err := git.PlainClone(clonePath, false, &git.CloneOptions{
		URL: repoUrl,
	})
	if err != nil && err != git.ErrRepositoryAlreadyExists {
		checkIfError(err)
	}

	if err == git.ErrRepositoryAlreadyExists {
		r, err = git.PlainOpen(clonePath)
		checkIfError(err)
	}

	err = r.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
		Force:      true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		checkIfError(err)
	}

	return r
}

func checkIfError(err error) {
	if err == storer.ErrStop {
		return
	}

	if err != nil {
		logger.Log.Fatal(
			"Error",
			logger.String("error", err.Error()),
		)
	}
}

func prettyPrint(v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logger.Log.Fatal("Failed to marshal JSON", logger.String("error", err.Error()))
		return
	}
	logger.Log.Info(string(b))
}

func dryRun() bool {
	var dryRun bool

	if len(os.Getenv("DRY_RUN")) == 0 {
		dryRun = true
	} else {
		dryRun = os.Getenv("DRY_RUN") == "true"
	}

	return dryRun
}
