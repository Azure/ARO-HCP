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
	"fmt"
	"strings"
)

// This file holds the body of the comment the preview leaves on the pull
// request, and the lines it writes to the job log.

// commentMarker opens the preview comment. The preview finds its own comment by
// this marker, so that a comment from an earlier run is updated instead of a
// second one being added.
const commentMarker = "<!-- grafana-preview -->"

// shortSHALength is how much of a commit is shown in the preview comment.
const shortSHALength = 7

// summaryRule separates the preview links in the job log.
const summaryRule = "============================================"

// dashboardLink is one uploaded dashboard, as the comment and the job log name it.
type dashboardLink struct {
	Title string
	URL   string
}

// printNoChange reports that the pull request changes no dashboard.
func printNoChange() {
	fmt.Println("No Grafana dashboard changes detected, skipping preview.")
}

// printChangedDashboards reports which dashboards the preview means to upload.
// It runs before the first Azure call, so that a job which cannot reach Grafana
// still shows them.
func printChangedDashboards(changed []string) {
	fmt.Println("Changed dashboards:")
	for _, file := range changed {
		fmt.Println(file)
	}
	fmt.Println()
}

// printUploading reports one dashboard as it goes up.
func printUploading(title, file string) {
	fmt.Printf("Uploading: %s (%s)\n", title, file)
}

// printSummary reports the preview links, so that they reach the job log even
// when the pull request comment cannot be written.
func printSummary(folderURL string, links []dashboardLink) {
	fmt.Println()
	fmt.Println(summaryRule)
	fmt.Println("Grafana Dashboard Preview Links")
	fmt.Println(summaryRule)
	fmt.Printf("Folder: %s\n", folderURL)
	for _, link := range links {
		fmt.Println(buildMarkdownLink(link))
	}
	fmt.Println(summaryRule)
}

// printNoGitHubToken reports that the preview links reached the job log only.
func printNoGitHubToken() {
	fmt.Printf("%s not set, skipping PR comment.\n", githubTokenVariable)
}

// buildCommentBody renders the comment that reports a preview. The marker opens
// the body, because comments are matched on it.
func buildCommentBody(folderURL string, links []dashboardLink, headSHA string) string {
	var builder strings.Builder

	builder.WriteString(commentMarker)
	builder.WriteString("\n## Grafana Dashboard Preview\n\n")
	builder.WriteString("The following Grafana dashboards have been deployed for preview:\n\n")
	for _, link := range links {
		builder.WriteString(buildMarkdownLink(link))
		builder.WriteString("\n")
	}
	builder.WriteString(fmt.Sprintf("\n:file_folder: [View all in PR folder](%s)\n", folderURL))
	builder.WriteString(fmt.Sprintf("\n*Preview updated for commit %s*", getShortSHA(headSHA)))

	return builder.String()
}

// buildMarkdownLink renders one dashboard as a markdown list item.
func buildMarkdownLink(link dashboardLink) string {
	return fmt.Sprintf("- [%s](%s)", escapeMarkdownLinkText(link.Title), link.URL)
}

// escapeMarkdownLinkText prepares a dashboard title to stand as the text of a
// markdown link. It puts the title on one line and escapes the brackets that
// would otherwise end the link text early.
func escapeMarkdownLinkText(title string) string {
	replacer := strings.NewReplacer(
		"\n", " ",
		"\\", "\\\\",
		"[", "\\[",
		"]", "\\]",
	)
	return replacer.Replace(title)
}

// getShortSHA abbreviates a commit the way git does.
func getShortSHA(sha string) string {
	if len(sha) <= shortSHALength {
		return sha
	}
	return sha[:shortSHALength]
}
