// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

const marker = "<!-- grafana-preview -->"

// writeRequest is one write the test server observed.
type writeRequest struct {
	Method string
	Path   string
	Body   string
}

// commentServer serves the comment endpoints of one pull request and records
// every write.
type commentServer struct {
	t *testing.T
	// pages holds the comment pages the list endpoint serves in order.
	pages [][]Comment
	// listStatus, when set, is returned by the list endpoint instead of a page.
	listStatus int
	// writeStatus, when set, is returned by every write endpoint.
	writeStatus int

	writes []writeRequest
}

func (s *commentServer) start() *Client {
	s.t.Helper()

	server := httptest.NewServer(http.HandlerFunc(s.serve))
	s.t.Cleanup(server.Close)

	api := gh.NewClient(server.Client())
	base, err := url.Parse(server.URL + "/")
	if err != nil {
		s.t.Fatalf("parse server URL: %v", err)
	}
	api.BaseURL = base

	client, err := NewClientForAPI(api, "Azure", "ARO-HCP")
	if err != nil {
		s.t.Fatalf("NewClientForAPI: %v", err)
	}
	return client
}

func (s *commentServer) serve(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		s.t.Errorf("read request body: %v", err)
	}

	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/repos/Azure/ARO-HCP/issues/7/comments":
		s.serveList(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/repos/Azure/ARO-HCP/issues/7/comments",
		request.Method == http.MethodPatch && strings.HasPrefix(request.URL.Path, "/repos/Azure/ARO-HCP/issues/comments/"),
		request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/repos/Azure/ARO-HCP/issues/comments/"):
		s.writes = append(s.writes, writeRequest{Method: request.Method, Path: request.URL.Path, Body: string(body)})
		s.serveWrite(writer, body)
	default:
		s.t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (s *commentServer) serveList(writer http.ResponseWriter, request *http.Request) {
	if s.listStatus != 0 {
		writer.WriteHeader(s.listStatus)
		return
	}

	page := 1
	if raw := request.URL.Query().Get("page"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &page); err != nil {
			s.t.Errorf("parse page %q: %v", raw, err)
		}
	}
	if page < 1 || page > len(s.pages) {
		s.t.Errorf("request for page %d, which the server does not hold", page)
		writer.WriteHeader(http.StatusNotFound)
		return
	}

	if page < len(s.pages) {
		writer.Header().Set("Link",
			fmt.Sprintf(`<https://api.github.com/repos/Azure/ARO-HCP/issues/7/comments?page=%d>; rel="next"`, page+1))
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(toAPIComments(s.pages[page-1])); err != nil {
		s.t.Errorf("encode comment page: %v", err)
	}
}

func (s *commentServer) serveWrite(writer http.ResponseWriter, body []byte) {
	if s.writeStatus != 0 {
		writer.WriteHeader(s.writeStatus)
		return
	}

	var sent struct {
		Body string `json:"body"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &sent); err != nil {
			s.t.Errorf("decode write body: %v", err)
		}
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(toAPIComment(fromApp(99, sent.Body))); err != nil {
		s.t.Errorf("encode write response: %v", err)
	}
}

// apiComment is the subset of GitHub's comment representation these tests serve.
type apiComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

func toAPIComment(comment Comment) apiComment {
	out := apiComment{ID: comment.ID, Body: comment.Body}
	out.User.Login = comment.Author
	out.User.Type = comment.AuthorType
	return out
}

// fromApp is a comment the preview wrote on an earlier run.
func fromApp(id int64, body string) Comment {
	return Comment{ID: id, Body: body, Author: "aro-bot", AuthorType: botAuthorType}
}

// fromPerson is a comment someone on the pull request wrote.
func fromPerson(id int64, author, body string) Comment {
	return Comment{ID: id, Body: body, Author: author, AuthorType: "User"}
}

func toAPIComments(comments []Comment) []apiComment {
	out := make([]apiComment, 0, len(comments))
	for _, comment := range comments {
		out = append(out, toAPIComment(comment))
	}
	return out
}

func TestListCommentsFollowsEveryPage(t *testing.T) {
	server := &commentServer{t: t, pages: [][]Comment{
		{fromPerson(1, "alice", "first")},
		{fromPerson(2, "bob", "second")},
		{fromApp(3, "third")},
	}}
	client := server.start()

	got, err := client.ListComments(t.Context(), 7)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}

	want := []Comment{
		fromPerson(1, "alice", "first"),
		fromPerson(2, "bob", "second"),
		fromApp(3, "third"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("ListComments = %v, want %v", got, want)
	}
}

func TestListCommentsReportsAFailure(t *testing.T) {
	server := &commentServer{t: t, listStatus: http.StatusInternalServerError}
	client := server.start()

	if _, err := client.ListComments(t.Context(), 7); err == nil {
		t.Fatal("ListComments succeeded, want an error")
	}
}

func TestUpsertMarkedComment(t *testing.T) {
	for _, test := range []struct {
		name       string
		pages      [][]Comment
		wantWrites []writeRequest
		wantID     int64
	}{
		{
			name:  "posts when nothing matches",
			pages: [][]Comment{{fromPerson(1, "alice", "a review comment")}},
			wantWrites: []writeRequest{
				{Method: http.MethodPost, Path: "/repos/Azure/ARO-HCP/issues/7/comments"},
			},
			wantID: 99,
		},
		{
			name:  "posts when the pull request has no comments",
			pages: [][]Comment{{}},
			wantWrites: []writeRequest{
				{Method: http.MethodPost, Path: "/repos/Azure/ARO-HCP/issues/7/comments"},
			},
			wantID: 99,
		},
		{
			name: "updates the one match",
			pages: [][]Comment{{
				fromPerson(1, "alice", "a review comment"),
				fromApp(2, marker+"\nold preview"),
			}},
			wantWrites: []writeRequest{
				{Method: http.MethodPatch, Path: "/repos/Azure/ARO-HCP/issues/comments/2"},
			},
			wantID: 99,
		},
		{
			name: "deletes the older matches and updates the newest",
			pages: [][]Comment{{
				fromApp(5, marker+"\nsecond"),
				fromApp(1, marker+"\nfirst"),
				fromApp(9, marker+"\nthird"),
				fromPerson(4, "alice", "an unrelated comment"),
			}},
			wantWrites: []writeRequest{
				{Method: http.MethodDelete, Path: "/repos/Azure/ARO-HCP/issues/comments/1"},
				{Method: http.MethodDelete, Path: "/repos/Azure/ARO-HCP/issues/comments/5"},
				{Method: http.MethodPatch, Path: "/repos/Azure/ARO-HCP/issues/comments/9"},
			},
			wantID: 99,
		},
		{
			name: "finds a match on a later page",
			pages: [][]Comment{
				{fromPerson(1, "alice", "a review comment")},
				{fromApp(2, marker+"\nold preview")},
			},
			wantWrites: []writeRequest{
				{Method: http.MethodPatch, Path: "/repos/Azure/ARO-HCP/issues/comments/2"},
			},
			wantID: 99,
		},
		{
			// Anyone can open a comment with the marker. Were it a match, this
			// run would delete the comment the last run left, and write the
			// preview into a comment that person can edit afterwards.
			name: "leaves a marked comment from a person alone",
			pages: [][]Comment{{
				fromPerson(2, "a-squatter", marker+"\nnot really the preview"),
			}},
			wantWrites: []writeRequest{
				{Method: http.MethodPost, Path: "/repos/Azure/ARO-HCP/issues/7/comments"},
			},
			wantID: 99,
		},
		{
			name: "prefers the app comment over a newer marked comment from a person",
			pages: [][]Comment{{
				fromApp(2, marker+"\nold preview"),
				fromPerson(8, "a-squatter", marker+"\nnot really the preview"),
			}},
			wantWrites: []writeRequest{
				{Method: http.MethodPatch, Path: "/repos/Azure/ARO-HCP/issues/comments/2"},
			},
			wantID: 99,
		},
		{
			name: "ignores a comment that only mentions the marker",
			pages: [][]Comment{{
				fromApp(2, "why does this say "+marker+"?"),
			}},
			wantWrites: []writeRequest{
				{Method: http.MethodPost, Path: "/repos/Azure/ARO-HCP/issues/7/comments"},
			},
			wantID: 99,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &commentServer{t: t, pages: test.pages}
			client := server.start()

			body := marker + "\nthe new preview"
			got, err := client.UpsertMarkedComment(t.Context(), 7, marker, body)
			if err != nil {
				t.Fatalf("UpsertMarkedComment: %v", err)
			}
			if got.ID != test.wantID {
				t.Errorf("comment ID = %d, want %d", got.ID, test.wantID)
			}

			if len(server.writes) != len(test.wantWrites) {
				t.Fatalf("writes = %v, want %v", server.writes, test.wantWrites)
			}
			for i, want := range test.wantWrites {
				if server.writes[i].Method != want.Method || server.writes[i].Path != want.Path {
					t.Errorf("write %d = %s %s, want %s %s",
						i, server.writes[i].Method, server.writes[i].Path, want.Method, want.Path)
				}
				if want.Method == http.MethodDelete {
					continue
				}
				if !strings.Contains(server.writes[i].Body, "the new preview") {
					t.Errorf("write %d body = %s, want it to carry the new body", i, server.writes[i].Body)
				}
			}
		})
	}
}

func TestUpsertMarkedCommentReportsAFailedWrite(t *testing.T) {
	server := &commentServer{
		t:           t,
		pages:       [][]Comment{{fromApp(2, marker+"\nold preview")}},
		writeStatus: http.StatusForbidden,
	}
	client := server.start()

	if _, err := client.UpsertMarkedComment(t.Context(), 7, marker, marker+"\nnew"); err == nil {
		t.Fatal("UpsertMarkedComment succeeded, want an error")
	}
}

func TestUpsertMarkedCommentRequiresAMarker(t *testing.T) {
	server := &commentServer{t: t, pages: [][]Comment{{}}}
	client := server.start()

	if _, err := client.UpsertMarkedComment(t.Context(), 7, "", "body"); err == nil {
		t.Fatal("UpsertMarkedComment succeeded, want an error")
	}
}

func TestNewClientRejectsIncompleteInput(t *testing.T) {
	if _, err := NewClient("", "Azure", "ARO-HCP"); err == nil {
		t.Error("NewClient accepted an empty token")
	}
	if _, err := NewClientForAPI(nil, "Azure", "ARO-HCP"); err == nil {
		t.Error("NewClientForAPI accepted a nil API client")
	}
	if _, err := NewClientForAPI(gh.NewClient(nil), "", "ARO-HCP"); err == nil {
		t.Error("NewClientForAPI accepted an empty owner")
	}
	if _, err := NewClientForAPI(gh.NewClient(nil), "Azure", ""); err == nil {
		t.Error("NewClientForAPI accepted an empty repository name")
	}
}
