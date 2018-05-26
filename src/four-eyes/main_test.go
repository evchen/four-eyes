package foureyes

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/github"
)

func TestSettingStatus(t *testing.T) {
	ref := getTestEnv(t, "REF")
	sha := getTestEnv(t, "SHA")
	repoOwner := getTestEnv(t, "REPO_OWNER")
	repo := getTestEnv(t, "REPO")

	ctx := context.Background()

	itr, err := ghinstallation.New(http.DefaultTransport, integrationID, installationID, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	event := PushEventData{
		Ref:     ref,
		HeadSHA: sha,
	}
	event.Repository.Name = repo
	event.Repository.Owner.Name = repoOwner

	client := github.NewClient(&http.Client{Transport: itr})
	if err := setMergeCommitStatus(ctx, client, event); err != nil {
		t.Error(err)
	}
}

func getTestEnv(t *testing.T, envSuffix string) string {
	env := "FOUR_EYES_" + envSuffix
	v := os.Getenv(env)
	if v == "" {
		t.Skip(env + " environment variable not defined.")
	}
	return v
}
