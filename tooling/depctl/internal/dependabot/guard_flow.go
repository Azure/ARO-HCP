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

package dependabot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RawAPI fetches one repository GET endpoint and returns its raw JSON body.
type RawAPI func(ctx context.Context, endpoint string) ([]byte, error)

// GuardCreate validates proposals, defers open overlaps, then schedules
// disjoint manifests. It rewrites output in place.
func GuardCreate(ctx context.Context, repo string, output map[string]any, ownedPRs []int, raw RawAPI, warn func(string)) error {
	if errorsField, ok := output["errors"].([]any); ok && len(errorsField) > 0 {
		return fmt.Errorf("%w: safe-output ingestion errors must be resolved before publication", ErrGuard)
	}
	items, _ := output["items"].([]any)
	kept := make([]any, 0, len(items))
	skipped := 0
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}
		if item["type"] == "create_pull_request" {
			if _, present := item["data"]; !present {
				title, _ := item["title"].(string)
				if title == "" {
					title = "untitled"
				}
				warn(fmt.Sprintf("Skipping create_pull_request without dependency group data: %s", title))
				skipped++
				continue
			}
		}
		kept = append(kept, entry)
	}
	if skipped > 0 {
		output["items"] = kept
	}
	items, _ = output["items"].([]any)
	var proposals []map[string]any
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if ok && item["type"] == "create_pull_request" {
			proposals = append(proposals, item)
		}
	}
	if len(proposals) == 0 {
		ensureNoop(output)
		return nil
	}
	candidates := make([]Candidate, len(proposals))
	for index, item := range proposals {
		candidate, err := Group(item)
		if err != nil {
			return err
		}
		candidates[index] = candidate
	}
	for index, candidate := range candidates {
		for _, other := range candidates[:index] {
			if FamilyMatches(candidate.Family, other.Family) || FamilyMatches(other.Family, candidate.Family) {
				return fmt.Errorf("%w: multiple new PRs for the same family", ErrGuard)
			}
		}
	}
	deferred := map[int]bool{}
	for _, number := range ownedPRs {
		files, err := pullRequestFiles(ctx, repo, number, raw)
		if err != nil {
			return err
		}
		hasNPM := false
		for _, candidate := range candidates {
			if candidate.Ecosystem == "npm" {
				hasNPM = true
			}
		}
		if hasNPM {
			if err := loadLockfiles(ctx, repo, number, files, raw); err != nil {
				return err
			}
		}
		for index, candidate := range candidates {
			reason := overlaps(candidate, files)
			if reason != "" {
				deferred[index] = true
				warn(fmt.Sprintf("Skipping create_pull_request for %s: open PR #%d: %s",
					candidate.Family, number, reason))
			}
		}
	}
	reserved := map[string]string{}
	for index, candidate := range candidates {
		if deferred[index] {
			continue
		}
		var shared []string
		for manifest := range candidate.Manifests {
			if _, ok := reserved[manifest]; ok {
				shared = append(shared, manifest)
			}
		}
		if len(shared) > 0 {
			sort.Strings(shared)
			families := map[string]bool{}
			for _, manifest := range shared {
				families[reserved[manifest]] = true
			}
			names := make([]string, 0, len(families))
			for family := range families {
				names = append(names, family)
			}
			sort.Strings(names)
			deferred[index] = true
			warn(fmt.Sprintf("Skipping create_pull_request for %s: same-batch manifest conflict with selected dependency group(s) %s: %s",
				candidate.Family, strings.Join(names, ", "), strings.Join(shared, ", ")))
			continue
		}
		for manifest := range candidate.Manifests {
			reserved[manifest] = candidate.Family
		}
	}
	final := make([]any, 0, len(items))
	proposalIndex := 0
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if ok && item["type"] == "create_pull_request" {
			if deferred[proposalIndex] {
				proposalIndex++
				continue
			}
			proposalIndex++
		}
		final = append(final, entry)
	}
	output["items"] = final
	ensureNoop(output)
	fmt.Printf("Checked %d dependency groups against %d owned security PRs; deferred %d create(s) for open-PR or same-batch conflicts\n",
		len(candidates), len(ownedPRs), len(deferred))
	return nil
}

// ensureNoop emits the runtime's explicit noop when nothing remains.
func ensureNoop(output map[string]any) {
	items, _ := output["items"].([]any)
	if len(items) == 0 {
		output["items"] = []any{map[string]any{
			"type":    "noop",
			"message": "No dependency pull requests remain after publication guard deferrals.",
		}}
	}
}

// pullRequestFiles reads one page of a pull request's files.
func pullRequestFiles(ctx context.Context, repo string, number int, raw RawAPI) ([]map[string]any, error) {
	body, err := raw(ctx, fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", repo, number))
	if err != nil {
		return nil, err
	}
	var files []map[string]any
	if err := json.Unmarshal(body, &files); err != nil {
		return nil, err
	}
	return files, nil
}

var (
	lockfileNames = map[string]bool{"package-lock.json": true, "npm-shrinkwrap.json": true}
	credentialURL = regexp.MustCompile(`https://[^\s)]+`)
	changedToken  = regexp.MustCompile(`[@a-zA-Z0-9._/+*-]+`)
)

// loadLockfiles reads base/head resolutions without including unrelated base moves.
func loadLockfiles(ctx context.Context, repo string, number int, files []map[string]any, raw RawAPI) error {
	locks := []map[string]any{}
	for _, file := range files {
		filename := stringField(file, "filename")
		previous := stringField(file, "previous_filename")
		if lockfileNames[baseName(filename)] || lockfileNames[baseName(previous)] {
			locks = append(locks, file)
		}
	}
	if len(locks) == 0 {
		return nil
	}
	detail, err := jsonObject(ctx, raw, fmt.Sprintf("repos/%s/pulls/%d", repo, number))
	if err != nil {
		return err
	}
	baseSHA := nestedString(detail, "base", "sha")
	headSHA := nestedString(detail, "head", "sha")
	comparison, err := jsonObject(ctx, raw, fmt.Sprintf("repos/%s/compare/%s...%s", repo, baseSHA, headSHA))
	if err != nil {
		return err
	}
	mergeBase := nestedString(comparison, "merge_base_commit", "sha")
	for _, file := range locks {
		var documents []map[string]LockEntry
		failed := false
		for _, side := range []string{"base", "head"} {
			status := stringField(file, "status")
			if (side == "base" && status == "added") || (side == "head" && status == "removed") {
				documents = append(documents, map[string]LockEntry{})
				continue
			}
			filePath := stringField(file, "filename")
			if side == "base" {
				filePath = stringField(file, "previous_filename")
				if filePath == "" {
					filePath = stringField(file, "filename")
				}
			}
			if !lockfileNames[baseName(filePath)] {
				documents = append(documents, map[string]LockEntry{})
				continue
			}
			ref := mergeBase
			if side == "head" {
				ref = headSHA
			}
			content, err := jsonObject(ctx, raw, fmt.Sprintf("repos/%s/contents/%s?ref=%s", repo, url.PathEscape(filePath), ref))
			if err != nil {
				failed = true
				break
			}
			if stringField(content, "encoding") != "base64" {
				failed = true
				break
			}
			decoded, err := base64.StdEncoding.DecodeString(stringField(content, "content"))
			if err != nil {
				failed = true
				break
			}
			var document map[string]any
			if err := json.Unmarshal(decoded, &document); err != nil {
				failed = true
				break
			}
			versions, err := LockVersions(document)
			if err != nil {
				failed = true
				break
			}
			documents = append(documents, versions)
		}
		if failed || len(documents) != 2 {
			file["lock_versions"] = nil
			continue
		}
		file["lock_versions"] = []any{documents[0], documents[1]}
	}
	return nil
}

// overlaps reports why an open pull request already covers a candidate.
func overlaps(candidate Candidate, files []map[string]any) string {
	manifestNames := map[string]bool{"go.mod": true}
	if candidate.Ecosystem == "npm" {
		manifestNames = map[string]bool{"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true}
	}
	for _, file := range files {
		filename := stringField(file, "filename")
		paths := map[string]bool{filename: true}
		if previous := stringField(file, "previous_filename"); previous != "" {
			paths[previous] = true
		}
		relevant := false
		lockfile := false
		for filePath := range paths {
			if manifestNames[baseName(filePath)] {
				relevant = true
			}
			if lockfileNames[baseName(filePath)] {
				lockfile = true
			}
		}
		if !relevant {
			continue
		}
		if lockfile {
			for filePath := range paths {
				paths[baseNameDir(filePath)+"/package.json"] = true
			}
		}
		packages, err := changedDependencies(candidate, file, lockfile)
		if err != nil {
			patch, _ := file["patch"].(string)
			var tokens []string
			for _, line := range strings.Split(patch, "\n") {
				if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "-") {
					continue
				}
				tokens = append(tokens, changedToken.FindAllString(line[1:], -1)...)
			}
			relevantManifest := false
			for filePath := range paths {
				if candidate.Manifests[filePath] {
					relevantManifest = true
				}
			}
			if relevantManifest {
				return fmt.Sprintf("%s: %v", filename, err)
			}
			for _, token := range tokens {
				if FamilyMatches(candidate.Family, token) {
					return fmt.Sprintf("%s: %v", filename, err)
				}
			}
			continue
		}
		var matching []string
		for pkg := range packages {
			if FamilyMatches(candidate.Family, pkg) {
				matching = append(matching, pkg)
			}
		}
		if len(matching) > 0 {
			sort.Strings(matching)
			return fmt.Sprintf("%s changes dependency %s", filename, strings.Join(matching, ", "))
		}
	}
	return ""
}

// changedDependencies extracts packages changed by one file.
func changedDependencies(candidate Candidate, file map[string]any, lockfile bool) (map[string]bool, error) {
	if lockfile {
		rawVersions, ok := file["lock_versions"].([]any)
		if !ok || len(rawVersions) != 2 {
			return nil, errors.New("complete npm lockfile comparison unavailable")
		}
		before, okBefore := rawVersions[0].(map[string]LockEntry)
		after, okAfter := rawVersions[1].(map[string]LockEntry)
		if !okBefore || !okAfter {
			return nil, errors.New("complete npm lockfile comparison unavailable")
		}
		packages := map[string]bool{}
		for packagePath := range before {
			if _, found := after[packagePath]; !found {
				packages[before[packagePath].Package] = true
			}
		}
		for packagePath := range after {
			if _, found := before[packagePath]; !found {
				packages[after[packagePath].Package] = true
			}
		}
		return packages, nil
	}
	hunks, err := PatchHunks(file)
	if err != nil {
		return nil, err
	}
	if candidate.Ecosystem == "go" {
		return GoDependencies(hunks)
	}
	return NpmDependencies(hunks)
}

// Status is one commit status entry.
type Status struct {
	Context   string `json:"context"`
	State     string `json:"state"`
	TargetURL string `json:"target_url"`
	CreatedAt string `json:"created_at"`
}

// CheckRun is one check-run entry.
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
}

// Comment is one issue comment.
type Comment struct {
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// GuardRetest proves a retest request carries a current failure and comparison.
func GuardRetest(ctx context.Context, repo string, number int, sha, evidence string, raw RawAPI, now time.Time) error {
	statusBody, err := raw(ctx, fmt.Sprintf("repos/%s/commits/%s/statuses?per_page=100", repo, sha))
	if err != nil {
		return err
	}
	var statuses []Status
	if err := json.Unmarshal(statusBody, &statuses); err != nil {
		return err
	}
	checkBody, err := raw(ctx, fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100&filter=latest", repo, sha))
	if err != nil {
		return err
	}
	var checkResponse struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	if err := json.Unmarshal(checkBody, &checkResponse); err != nil {
		return err
	}
	latest := map[string]Status{}
	for _, status := range statuses {
		if strings.EqualFold(status.Context, "tide") {
			continue
		}
		if current, ok := latest[status.Context]; !ok || status.CreatedAt > current.CreatedAt {
			latest[status.Context] = status
		}
	}
	failed := map[string]bool{}
	for _, status := range latest {
		if status.State == "failure" || status.State == "error" {
			if status.TargetURL != "" {
				failed[trimURL(status.TargetURL)] = true
			}
		}
	}
	for _, check := range checkResponse.CheckRuns {
		switch check.Conclusion {
		case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
			if check.DetailsURL != "" {
				failed[trimURL(check.DetailsURL)] = true
			}
		}
	}
	links := map[string]bool{}
	for _, link := range credentialURL.FindAllString(evidence, -1) {
		links[trimURL(link)] = true
	}
	hasFailed := false
	for link := range links {
		if failed[link] {
			hasFailed = true
		}
	}
	if !hasFailed {
		return fmt.Errorf("%w: retest needs a current failing check URL and proven transient-failure evidence", ErrGuard)
	}
	extra := false
	for link := range links {
		if !failed[link] {
			extra = true
		}
	}
	if !extra {
		return fmt.Errorf("%w: retest also needs a comparison run URL", ErrGuard)
	}
	commentsBody, err := raw(ctx, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", repo, number))
	if err != nil {
		return err
	}
	var comments []Comment
	if err := json.Unmarshal(commentsBody, &comments); err != nil {
		return err
	}
	for _, comment := range comments {
		if !strings.Contains(comment.Body, "/retest-required") {
			continue
		}
		posted, err := time.Parse(time.RFC3339, comment.CreatedAt)
		if err == nil && posted.After(now.Add(-24*time.Hour)) {
			return fmt.Errorf("%w: PR #%d already requested a retest in the last 24 hours", ErrGuard, number)
		}
	}
	return nil
}

// trimURL removes trailing punctuation from an evidence link.
func trimURL(value string) string {
	return strings.TrimRight(value, ".;,")
}

// jsonObject decodes one JSON object endpoint.
func jsonObject(ctx context.Context, raw RawAPI, endpoint string) (map[string]any, error) {
	body, err := raw(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// nestedString reads a nested string field.
func nestedString(value map[string]any, path ...string) string {
	current := value
	for index, part := range path {
		if index == len(path)-1 {
			return stringField(current, part)
		}
		next, _ := current[part].(map[string]any)
		if next == nil {
			return ""
		}
		current = next
	}
	return ""
}

// stringField reads one optional string field.
func stringField(value map[string]any, field string) string {
	text, _ := value[field].(string)
	return text
}

// baseName returns the final path element.
func baseName(file string) string {
	if index := strings.LastIndex(file, "/"); index >= 0 {
		return file[index+1:]
	}
	return file
}

// baseNameDir returns the directory prefix, or an empty string at the root.
func baseNameDir(file string) string {
	if index := strings.LastIndex(file, "/"); index >= 0 {
		return file[:index]
	}
	return ""
}
