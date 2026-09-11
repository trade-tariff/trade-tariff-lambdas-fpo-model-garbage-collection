package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/aws/aws-sdk-go/aws"         //nolint:staticcheck // aws-sdk-go v1 still in use until v2 migration
	"github.com/aws/aws-sdk-go/aws/session" //nolint:staticcheck // aws-sdk-go v1 still in use until v2 migration
	"github.com/aws/aws-sdk-go/service/s3"  //nolint:staticcheck // aws-sdk-go v1 still in use until v2 migration
)

const (
	bucket    = "trade-tariff-models-382373577178"
	repoUrl   = "https://github.com/trade-tariff/trade-tariff-lambdas-fpo-search"
	clonePath = "/tmp/trade-tariff-lambdas-fpo-search"
)

// objectLister is the slice of the S3 client this collector needs. It exists so
// the listing logic can be exercised against a faked, multi-page response: the
// bug this interface was introduced for only shows up past the first page.
type objectLister interface {
	ListObjectsV2Pages(input *s3.ListObjectsV2Input, fn func(*s3.ListObjectsV2Output, bool) bool) error
}

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
	// Resolved before any work so a misconfigured deployment fails immediately
	// rather than after a full listing.
	dryRun, err := parseDryRun(os.LookupEnv("DRY_RUN"))
	checkIfError(err)

	client := s3.New(initializeAWSSession())
	repo := fetchRepo()
	relevantBranches := fetchRemoteBranches(*repo)
	relevantCommits := fetchRemoteCommits(*repo, relevantBranches)

	relevantModels, err := fetchS3ModelVersions(client, relevantCommits)
	checkIfError(err)

	if dryRun {
		logger.Log.Info("DRY_RUN is true, no objects will be deleted")
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
func fetchS3ModelVersions(client objectLister, outstandingCommits []*object.Commit) (map[string]Model, error) {
	models := make(map[string]Model)
	pattern := `^(\d+\.\d+\.\d+)-([a-f0-9]{7})/.*$`
	bucket := "trade-tariff-models-382373577178"
	objectCount := 0

	re := regexp.MustCompile(pattern)

	// S3 caps a listing at 1000 keys per response. Paging through every page is
	// what makes the classification below trustworthy: a truncated listing looks
	// exactly like a complete one, so a deployment marker sitting past the cut
	// would leave a live model looking undeployed and therefore deletable.
	err := client.ListObjectsV2Pages(
		&s3.ListObjectsV2Input{Bucket: aws.String(bucket)},
		func(page *s3.ListObjectsV2Output, _ bool) bool {
			for _, obj := range page.Contents {
				if obj.Key == nil {
					continue
				}
				objectCount++

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

			// Always continue: stopping early would reintroduce a partial view.
			return true
		},
	)
	if err != nil {
		return nil, fmt.Errorf("listing objects in bucket %s: %w", bucket, err)
	}

	logger.Log.Info(
		"Listed bucket objects",
		logger.String("bucket", bucket),
		logger.Int("objects", objectCount),
		logger.Int("modelVersions", len(models)),
	)

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

	return relevant_models, nil
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

// parseDryRun demands an explicit "true" or "false".
//
// Defaulting an unset DRY_RUN to true used to look identical to a healthy run:
// the collector reported success every day while collecting nothing. Anything
// other than the two accepted values used to fall through to false, so a typo
// such as DRY_RUN=flase silently armed deletion. Both failure modes are now
// invocation errors, which the deploy already satisfies: the Makefile sets
// DRY_RUN=true for development and staging and DRY_RUN=false for production,
// and serverless.yml passes it straight through to the function environment.
func parseDryRun(value string, present bool) (bool, error) {
	if !present {
		return false, errors.New("DRY_RUN is not set, set it to \"true\" or \"false\"")
	}

	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("DRY_RUN must be \"true\" or \"false\", got %q", value)
	}
}
