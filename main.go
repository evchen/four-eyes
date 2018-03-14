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
	"strings"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/github"
	"google.golang.org/appengine"
	"google.golang.org/appengine/urlfetch"
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
		w.WriteHeader(204)
	}
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	if err := decodeJSONOrBail(w, r, &struct{}{}); err != nil {
		return
	}
	w.WriteHeader(204)
}

func handlePushEvent(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)
	var event github.PushEvent

	if err := decodeJSONOrBail(w, r, &event); err != nil {
		return
	}

	if !isRelevantRef(event.GetRef()) {
		return
	}

	appengineTransport := urlfetch.Transport{ctx, false}
	itr, err := ghinstallation.New(&appengineTransport, integrationID, installationID, privateKey)
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
		client.Repositories.CreateStatus(ctx, event.GetRepo().GetOwner().GetName(), event.GetRepo().GetName(), event.GetHeadCommit().GetID(), &github.RepoStatus{
			State:       &state,
			Description: &desc,
			Context:     &context,
		})
	}
	w.WriteHeader(204)
}

func setMergeCommitStatus(ctx context.Context, client *github.Client, data github.PushEvent) error {
	pullRequestNumbers := extractFailedPullRequestNumbers(data.GetHeadCommit().GetMessage())

	var rejectedPrs []string
	for _, pullRequestNumber := range pullRequestNumbers {
		rejectedPrs = append(rejectedPrs, fmt.Sprintf("#%s", pullRequestNumber))
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

	_, _, err := client.Repositories.CreateStatus(ctx, data.GetRepo().GetOwner().GetName(), data.GetRepo().GetName(), data.GetHeadCommit().GetID(), &status)
	return err
}

// Source: https://www.npmjs.com/package/github-username-regex
var prRegexp = regexp.MustCompile(`(\d+): .* r=(\S+) a=(\S+)`)

func extractFailedPullRequestNumbers(commitMessage string) []string {
	matches := prRegexp.FindAllStringSubmatch(commitMessage, -1)
	var result []string
	for _, match := range matches {
		if match[2] == match[3] {
			result = append(result, match[1])
		}
	}
	return result
}

// Utility functions.

func isRelevantRef(ref string) bool {
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
