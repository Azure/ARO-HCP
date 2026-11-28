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

package preview

import (
	"strings"
	"testing"
)

func TestEscapeMarkdownLinkText(t *testing.T) {
	for _, test := range []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "a plain title is unchanged",
			title: "Backend",
			want:  "Backend",
		},
		{
			name:  "brackets are escaped so they cannot end the link text",
			title: "Backend [beta]",
			want:  "Backend \\[beta\\]",
		},
		{
			name:  "a newline is flattened so the list item stays on one line",
			title: "Backend\nOverview",
			want:  "Backend Overview",
		},
		{
			name:  "a title that is itself a link is defused",
			title: "[click](https://example.com)",
			want:  "\\[click\\](https://example.com)",
		},
		{
			// Without escaping the backslash, the output holds "\\]", which
			// markdown reads as a literal backslash and a live bracket. The
			// bracket closes the link text, and the URL that follows becomes
			// the link target.
			name:  "a trailing backslash cannot escape the closing bracket",
			title: "Backend\\",
			want:  "Backend\\\\",
		},
		{
			name:  "a backslash before a bracket cannot open a link",
			title: "Backend\\](https://evil.example) [details",
			want:  "Backend\\\\\\](https://evil.example) \\[details",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := escapeMarkdownLinkText(test.title); got != test.want {
				t.Errorf("escapeMarkdownLinkText(%q) = %q, want %q", test.title, got, test.want)
			}
		})
	}
}

func TestGetShortSHA(t *testing.T) {
	if got := getShortSHA("1a2b3c4d5e6f7890"); got != "1a2b3c4" {
		t.Errorf("getShortSHA = %q, want 1a2b3c4", got)
	}
	if got := getShortSHA("abc"); got != "abc" {
		t.Errorf("getShortSHA = %q, want abc for a commit shorter than the abbreviation", got)
	}
}

// TestBuildCommentBody pins the comment to the layout hack/ci/grafana-preview.sh
// produced, so that the move to Go does not change what a pull request author sees.
func TestBuildCommentBody(t *testing.T) {
	links := []dashboardLink{
		{Title: "Backend", URL: "https://grafana.example.com/d/pr-1234-backend"},
		{Title: "Frontend [beta]", URL: "https://grafana.example.com/d/pr-1234-frontend"},
	}

	got := buildCommentBody("https://grafana.example.com/dashboards/f/pr-1234", links, "1a2b3c4d5e6f7890abcdef")

	want := strings.Join([]string{
		"<!-- grafana-preview -->",
		"## Grafana Dashboard Preview",
		"",
		"The following Grafana dashboards have been deployed for preview:",
		"",
		"- [Backend](https://grafana.example.com/d/pr-1234-backend)",
		"- [Frontend \\[beta\\]](https://grafana.example.com/d/pr-1234-frontend)",
		"",
		":file_folder: [View all in PR folder](https://grafana.example.com/dashboards/f/pr-1234)",
		"",
		"*Preview updated for commit 1a2b3c4*",
	}, "\n")

	if got != want {
		t.Errorf("buildCommentBody =\n%s\n\nwant\n%s", got, want)
	}
}

func TestBuildCommentBodyOpensWithTheMarker(t *testing.T) {
	body := buildCommentBody("https://grafana.example.com/dashboards/f/pr-1", nil, "abcdefg")
	if !strings.HasPrefix(body, commentMarker) {
		t.Errorf("comment body does not open with the marker, so the next run would not find it: %q", body)
	}
}
