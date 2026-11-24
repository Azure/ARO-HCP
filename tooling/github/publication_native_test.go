// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

// TestNativePublicationPreviousMarker authorizes an exact prior identity only until the write boundary.
func TestNativePublicationPreviousMarker(t *testing.T) {
	for _, mode := range []string{"update sources", "foreign author", "wrong previous marker", "stale readback", "ambiguous write", "changed head"} {
		t.Run(mode, func(t *testing.T) {
			const previous = `<!-- native-dependabot-maintenance:{"group":"group","sources":[1]} -->`
			const next = `<!-- native-dependabot-maintenance:{"group":"group","sources":[1,2]} -->`
			pull := testPull()
			pull.Body = gh.Ptr("old body\n" + previous)
			if mode == "foreign author" {
				pull.User.Login = gh.Ptr("human")
			}
			if mode == "changed head" {
				pull.Head.SHA = gh.Ptr(strings.Repeat("c", 40))
			}
			request := testPublicationRequest()
			request.Marker, request.PreviousMarker = next, previous
			request.Body = "new body\n" + next
			if mode == "wrong previous marker" {
				request.PreviousMarker = "<!-- unknown -->"
			}
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/user":
					respond(t, w, &gh.User{Login: gh.Ptr("robot")})
				case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/branches/"):
					sha := testHead
					if strings.HasSuffix(r.URL.Path, "/main") {
						sha = testBase
					}
					respond(t, w, &gh.Branch{Commit: &gh.RepositoryCommit{SHA: &sha}})
				case r.URL.Path == "/repos/owner/repo/pulls" && r.Method == http.MethodGet:
					respond(t, w, []*gh.PullRequest{testListedPull(pull)})
				case r.URL.Path == "/repos/owner/repo/pulls/7" && r.Method == http.MethodPatch:
					var fields map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
						t.Error(err)
					}
					if len(fields) != 2 || fields["title"] == nil || fields["body"] == nil {
						t.Errorf("unsafe update fields %v", fields)
					}
					if mode == "ambiguous write" {
						http.Error(w, "write may have completed", http.StatusBadGateway)
						return
					}
					if mode != "stale readback" {
						pull.Body = gh.Ptr(request.Body)
						pull.Title = gh.Ptr(request.Title)
					}
					respond(t, w, pull)
				case r.URL.Path == "/repos/owner/repo/pulls/7":
					respond(t, w, pull)
				case r.URL.Path == "/repos/owner/repo/pulls/7/files":
					respond(t, w, []*gh.CommitFile{{Filename: gh.Ptr("expected")}})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			})
			receipt, err := client.Publish(context.Background(), request)
			switch mode {
			case "update sources":
				if err != nil || receipt.Number != 7 || receipt.Outcome != "observed" || pull.GetBody() != request.Body {
					t.Fatalf("receipt=%+v err=%v body=%q", receipt, err, pull.GetBody())
				}
			case "stale readback", "ambiguous write":
				if !errors.Is(err, ErrIndeterminate) || receipt.Outcome != "indeterminate" {
					t.Fatalf("write uncertainty not preserved: %+v %v", receipt, err)
				}
			default:
				if !errors.Is(err, ErrConflict) || receipt.Outcome != "not-applicable" {
					t.Fatalf("ownership conflict not rejected: %+v %v", receipt, err)
				}
				if pull.GetBody() != "old body\n"+previous {
					t.Fatal("unowned proposal changed")
				}
			}
		})
	}
}

// TestNativePublicationPreviousMarkerValidation rejects malformed authorization markers before API access.
func TestNativePublicationPreviousMarkerValidation(t *testing.T) {
	for _, previous := range []string{"not a marker", "<!-- one -->\n<!-- two -->", "<!-- nested <!-- marker -->", "<!-- marker --> trailing"} {
		t.Run(previous, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid marker caused network access: %s", r.URL)
				http.NotFound(w, r)
			})
			request := testPublicationRequest()
			request.PreviousMarker = previous
			if _, err := client.Publish(context.Background(), request); err == nil {
				t.Fatal("malformed prior marker accepted")
			}
		})
	}
}
