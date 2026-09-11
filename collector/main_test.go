package main

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"        //nolint:staticcheck // aws-sdk-go v1 still in use until v2 migration
	"github.com/aws/aws-sdk-go/service/s3" //nolint:staticcheck // aws-sdk-go v1 still in use until v2 migration
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// fakeObjectLister replays a fixed set of pages the way the real SDK does:
// one callback per page, lastPage true only on the final one, and it stops
// early if the callback returns false.
type fakeObjectLister struct {
	pages [][]string
	err   error
	calls int
}

func (f *fakeObjectLister) ListObjectsV2Pages(
	_ *s3.ListObjectsV2Input,
	fn func(*s3.ListObjectsV2Output, bool) bool,
) error {
	for i, page := range f.pages {
		f.calls++

		contents := make([]*s3.Object, 0, len(page))
		for _, key := range page {
			contents = append(contents, &s3.Object{Key: aws.String(key)})
		}

		lastPage := i == len(f.pages)-1
		if !fn(&s3.ListObjectsV2Output{Contents: contents}, lastPage) {
			break
		}
	}

	return f.err
}

func commitsFor(shas ...string) []*object.Commit {
	commits := make([]*object.Commit, 0, len(shas))
	for _, sha := range shas {
		commits = append(commits, &object.Commit{Hash: plumbing.NewHash(sha)})
	}
	return commits
}

func TestFetchS3ModelVersions(t *testing.T) {
	tests := []struct {
		name               string
		pages              [][]string
		outstandingCommits []*object.Commit
		wantVersionKeys    []string
		wantKeyCounts      map[string]int
	}{
		{
			name: "a deployment marker on a later page protects the model",
			pages: [][]string{
				{"1.0.1-2112d82/model.pt", "1.0.1-2112d82/subheadings.pkl"},
				{"1.0.1-2112d82/production/current"},
			},
			wantVersionKeys: []string{},
		},
		{
			name: "a staging marker on a later page protects the model",
			pages: [][]string{
				{"1.0.1-2112d82/model.pt"},
				{"1.0.1-2112d82/staging/current"},
			},
			wantVersionKeys: []string{},
		},
		{
			name: "an undeployed model collects keys from every page",
			pages: [][]string{
				{"1.0.2-abc1234/model.pt"},
				{"1.0.2-abc1234/subheadings.pkl"},
				{"1.0.2-abc1234/vectoriser.pkl"},
			},
			wantVersionKeys: []string{"1.0.2-abc1234"},
			wantKeyCounts:   map[string]int{"1.0.2-abc1234": 3},
		},
		{
			name: "a model under active development on a later page is preserved",
			pages: [][]string{
				{"1.0.3-deadbee/model.pt"},
				{"1.0.3-deadbee/subheadings.pkl"},
			},
			outstandingCommits: commitsFor("deadbeef00000000000000000000000000000000"),
			wantVersionKeys:    []string{},
		},
		{
			name: "keys that do not match the version prefix are ignored",
			pages: [][]string{
				{"not-a-model/file.txt", "1.0.4-1234567/model.pt"},
				{"README.md"},
			},
			wantVersionKeys: []string{"1.0.4-1234567"},
			wantKeyCounts:   map[string]int{"1.0.4-1234567": 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lister := &fakeObjectLister{pages: test.pages}

			models, err := fetchS3ModelVersions(lister, test.outstandingCommits)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if lister.calls != len(test.pages) {
				t.Fatalf("expected every page to be listed, got %d of %d", lister.calls, len(test.pages))
			}

			if len(models) != len(test.wantVersionKeys) {
				t.Fatalf("expected %d deletable models, got %d: %v", len(test.wantVersionKeys), len(models), models)
			}

			for _, versionKey := range test.wantVersionKeys {
				model, exists := models[versionKey]
				if !exists {
					t.Fatalf("expected %q to be deletable, got %v", versionKey, models)
				}
				if want := test.wantKeyCounts[versionKey]; len(model.Keys) != want {
					t.Fatalf("expected %d keys for %q, got %d: %v", want, versionKey, len(model.Keys), model.Keys)
				}
			}
		})
	}
}

func TestFetchS3ModelVersionsReturnsListingErrors(t *testing.T) {
	lister := &fakeObjectLister{
		pages: [][]string{{"1.0.1-2112d82/model.pt"}},
		err:   errors.New("access denied"),
	}

	_, err := fetchS3ModelVersions(lister, nil)
	if err == nil {
		t.Fatal("expected a listing error to be returned, got nil")
	}
}

func TestParseDryRun(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		present    bool
		wantDryRun bool
		wantErr    bool
	}{
		{name: "explicitly true", value: "true", present: true, wantDryRun: true},
		{name: "explicitly false", value: "false", present: true, wantDryRun: false},
		{name: "unset is an error rather than a silent dry run", present: false, wantErr: true},
		{name: "empty is an error", value: "", present: true, wantErr: true},
		{name: "a typo is an error rather than a destructive run", value: "flase", present: true, wantErr: true},
		{name: "casing is not guessed at", value: "TRUE", present: true, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dryRun, err := parseDryRun(test.value, test.present)

			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q (present=%v), got dryRun=%v", test.value, test.present, dryRun)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dryRun != test.wantDryRun {
				t.Fatalf("expected dryRun=%v, got %v", test.wantDryRun, dryRun)
			}
		})
	}
}
