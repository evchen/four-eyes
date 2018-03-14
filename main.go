package hello

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/github"
)

func init() {
	http.HandleFunc("/webhook", webhookHandler)
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	event := r.Header.Get("X-GitHub-Event")
	switch event {
	case "ping":
		handlePing(w, r)
	case "push":
		// https://developer.github.com/v3/activity/events/types/#pushevent
		handlePushEvent(w, r)
	default:
		log.Println("unrecognized event:", event)
		w.WriteHeader(400)
	}
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	if err := decodeJSONOrBail(w, r, &struct{}{}); err != nil {
		return
	}
}

func handlePushEvent(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	var event PushEventData

	if err := decodeJSONOrBail(w, r, &event); err != nil {
		return
	}

	if !isRelevantRef(event.Ref) {
		log.Println("Came here")
		return
	}

	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, integrationID, privateKey)
	if err != nil {
		log.Println("Unable to create a new transport:", err)
		return
	}

	client := github.NewClient(&http.Client{Transport: itr})

	if err := setMergeCommitStatus(ctx, client, event); err != nil {
		log.Println(err)
		state := "failure"
		desc := "Something went wrong when checking who approved the PRs."
		context := "tink/four-eyes"
		client.Repositories.CreateStatus(ctx, event.Repository.Owner.Name, event.Repository.Name, event.HeadSHA, &github.RepoStatus{
			State:       &state,
			Description: &desc,
			Context:     &context,
		})
	}
}

type PushEventData struct {
	Ref        string `json:"ref"`
	HeadSHA    string `json:"head"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
	} `json:"repository"`
}

func setMergeCommitStatus(ctx context.Context, client *github.Client, data PushEventData) error {
	commit, _, err := client.Git.GetCommit(ctx, data.Repository.Owner.Name, data.Repository.Name, data.HeadSHA)
	if err != nil {
		return err
	}

	pullRequestNumbers := extractPullRequestNumbers(commit)

	var rejectedPrs []string
	for _, pullRequestNumber := range pullRequestNumbers {
		if !approvedByNonAuthor(ctx, client, data, pullRequestNumber) {
			rejectedPrs = append(rejectedPrs, fmt.Sprintf("#%d", pullRequestNumber))
		}
	}

	// TODO: Move to constant and replace all usages.
	appContext := "tink/four-eyes"

	var status github.RepoStatus
	status.Context = &appContext

	var state, desc string
	if len(rejectedPrs) == 0 {
		state = "success"
		desc = "All pull requests were reviewed by an peer other than the author."
	} else {
		state = "error"
		desc = "The following pull requests were not approved by a second peer: " + strings.Join(rejectedPrs, ",")
	}
	status.State = &state
	status.Description = &desc

	_, _, err = client.Repositories.CreateStatus(ctx, data.Repository.Owner.Name, data.Repository.Name, data.HeadSHA, &status)
	return err
}

// Source: https://www.npmjs.com/package/github-username-regex
var prRegexp = regexp.MustCompile(`(\d+): .* r=(\S+) a=(\S+)`)

func extractPullRequestNumbers(c *github.Commit) []int {
	matches := prRegexp.FindAllStringSubmatch(*c.Message, -1)
	var result []int
	for _, match := range matches {
		i, _ := strconv.Atoi(match[1])
		result = append(result, i)
	}
	return result
}

func approvedByNonAuthor(ctx context.Context, client *github.Client, data PushEventData, pr int) bool {
	pullRequest, _, err := client.PullRequests.Get(ctx, data.Repository.Owner.Name, data.Repository.Name, pr)
	if err != nil {
		log.Println("Could not fetch pull request.", "Pr:", pr, "Error:", err)
		return false
	}

	author := pullRequest.User.ID

	opt := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 10},
	}
	for {
		comments, resp, err := client.Issues.ListComments(ctx, data.Repository.Owner.Name, data.Repository.Name, pr, opt)
		if err != nil {
			log.Println("Could not fetch pull request comments.", "Pr:", pr, "Error:", err)
			return false
		}

		for _, comment := range comments {
			log.Println(strings.ToLower(strings.Trim(comment.GetBody(), " ")))
			if strings.ToLower(strings.Trim(comment.GetBody(), " ")) == "bors r+" && *comment.User.ID != *author {
				return true
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return false
}

// Utility functions.

func isRelevantRef(ref string) bool {
	log.Println(ref)
	return ref == "refs/heads/staging" || ref == "refs/heads/trying"
}

func decodeJSONOrBail(w http.ResponseWriter, r *http.Request, m interface{}) error {
	err := decodeAndValidateJSON(r, &m)
	if err != nil {
		log.Println(err)
		if err == errIncorrectSignature {
			w.WriteHeader(401)
			return err
		}
		w.WriteHeader(400)
	}
	return err
}

var errIncorrectSignature = errors.New("signature is incorrect")

func decodeAndValidateJSON(r *http.Request, m interface{}) error {
	givenHmacString := r.Header.Get("X-Hub-Signature")

	if givenHmacString == "" {
		return errIncorrectSignature
	}

	pieces := strings.SplitN(givenHmacString, "=", 2)
	if len(pieces) < 2 {
		return errors.New("malformed signature")
	}
	if pieces[0] != "sha1" {
		return errors.New("hmac type not supported: " + pieces[0])
	}

	givenHmac, err := hex.DecodeString(pieces[1])
	if err != nil {
		return err
	}

	hmacer := hmac.New(sha1.New, hmacSecret)
	teeReader := io.TeeReader(r.Body, hmacer)

	if err := json.NewDecoder(teeReader).Decode(m); err != nil {
		return err
	}

	expectedMAC := hmacer.Sum(nil)
	if !hmac.Equal(givenHmac, expectedMAC) {
		return errIncorrectSignature
	}

	return nil
}
