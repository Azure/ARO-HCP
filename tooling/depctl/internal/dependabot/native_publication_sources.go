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
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/go-github/v69/github"
)

var nativePublicationChecksum = regexp.MustCompile(`^\S+ v[0-9]\S* h1:[A-Za-z0-9+/]+=*$`)
var nativePublicationReleaseTag = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9_.-]+)?$`)

// NativePublicationRead adapts only the existing official release resolver to the injected App reader.
func (p *NativePublisher) nativePublicationRead(ctx context.Context, endpoint string, pages, ci bool) (json.RawMessage, error) {
	if pages || ci || !nativeReleaseEndpoint.MatchString(endpoint) {
		return nil, nativeReject("unsupported official release GET")
	}
	request, err := p.api.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("construct official release read: %w", err)
	}
	var raw json.RawMessage
	if _, err := p.api.Do(ctx, request, &raw); err != nil {
		return nil, fmt.Errorf("read official release: %w", err)
	}
	return raw, nil
}

// NativePublicationContent reads a regular immutable file and verifies complete Git blob bytes.
func (p *NativePublisher) nativePublicationContent(ctx context.Context, sha, filename string) (string, error) {
	if !nativeSHA.MatchString(sha) || !safeNativePath(filename) {
		return "", nativeReject("invalid immutable content identity")
	}
	file, directory, _, err := p.api.Repositories.GetContents(ctx, p.owner, p.repo, filename, &github.RepositoryContentGetOptions{Ref: sha})
	if err != nil {
		return "", fmt.Errorf("read immutable file %s at %s: %w", filename, sha, err)
	}
	if file == nil || directory != nil || file.GetType() != "file" || !nativeSHA.MatchString(file.GetSHA()) || file.Size == nil || file.GetSize() < 0 {
		return "", nativeReject("missing immutable regular file")
	}
	blobSHA := file.GetSHA()
	size := file.GetSize()
	encoding, encoded := file.GetEncoding(), file.Content
	if encoding == "none" {
		blob, _, err := p.api.Git.GetBlob(ctx, p.owner, p.repo, blobSHA)
		if err != nil {
			return "", fmt.Errorf("read large immutable blob %s: %w", filename, err)
		}
		if blob == nil || blob.GetSHA() != blobSHA || blob.Size == nil || blob.GetSize() != size {
			return "", nativeReject("large-file blob identity or size changed")
		}
		encoding, encoded = blob.GetEncoding(), blob.Content
	}
	if encoding != "base64" || encoded == nil {
		return "", nativeReject("missing complete immutable file content")
	}
	decoded, err := base64.StdEncoding.DecodeString(*encoded)
	if err != nil {
		return "", fmt.Errorf("decode immutable content %s: %w", filename, err)
	}
	if len(decoded) != size || !utf8.Valid(decoded) {
		return "", nativeReject("truncated or non-UTF-8 immutable content")
	}
	hash := sha1.New()
	_, _ = fmt.Fprintf(hash, "blob %d\x00", len(decoded))
	_, _ = hash.Write(decoded)
	if fmt.Sprintf("%x", hash.Sum(nil)) != blobSHA {
		return "", nativeReject("immutable content does not match blob identity")
	}
	return string(decoded), nil
}

// NativePublicationOfficialPin requires a complete, unique official release-tag inventory.
func (p *NativePublisher) nativePublicationOfficialPin(ctx context.Context, repository, pin string) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || !nativeSHA.MatchString(pin) {
		return nativeReject("invalid official action identity")
	}
	seen := map[string]bool{}
	found := false
	options := &github.ListOptions{Page: 1, PerPage: 100}
	for {
		tags, response, err := p.api.Repositories.ListTags(ctx, parts[0], parts[1], options)
		if err != nil {
			return fmt.Errorf("read official action release tags: %w", err)
		}
		if tags == nil || response == nil {
			return nativeReject("missing official action tag inventory page")
		}
		for _, tag := range tags {
			if tag == nil || tag.Name == nil || !nativeSHA.MatchString(tag.GetCommit().GetSHA()) || seen[tag.GetName()] {
				return nativeReject("malformed or duplicate official action tag inventory")
			}
			seen[tag.GetName()] = true
			found = found || tag.GetCommit().GetSHA() == pin && nativePublicationReleaseTag.MatchString(tag.GetName())
		}
		if response.NextPage == 0 {
			break
		}
		if response.NextPage != options.Page+1 {
			return nativeReject("nonadvancing official action tag pages")
		}
		options.Page = response.NextPage
	}
	if !found {
		return nativeReject("Actions target has no official immutable release-tag evidence")
	}
	return nil
}

// NativePublicationSourceIntent derives dependency intent exclusively from immutable merge-base and head files.
func (p *NativePublisher) nativePublicationSourceIntent(ctx context.Context, snapshot nativePublicationSnapshot) (map[string]any, error) {
	pull := snapshot.pull
	comparison, _, err := p.api.Repositories.CompareCommits(ctx, p.owner, p.repo, pull.GetBase().GetSHA(), pull.GetHead().GetSHA(), nil)
	if err != nil {
		return nil, fmt.Errorf("read native immutable merge base: %w", err)
	}
	base := comparison.GetMergeBaseCommit().GetSHA()
	if !nativeSHA.MatchString(base) {
		return nil, nativeReject("missing immutable merge base")
	}
	intents := make([]map[string]any, 0)
	ancillary := make([]string, 0)
	documents := map[string][2]string{}
	for _, file := range snapshot.files {
		filename := file.GetFilename()
		if !safeNativePath(filename) || file.GetStatus() != "modified" || file.GetPreviousFilename() != "" {
			return nil, nativeReject("native file additions/removals/renames need manual diagnosis")
		}
		if path.Base(filename) == "go.sum" || path.Base(filename) == "go.work.sum" {
			ancillary = append(ancillary, filename)
			continue
		}
		if nativeEcosystem(filename) == "" {
			return nil, nativeReject("unsupported/protected native source path: " + filename)
		}
		before, err := p.nativePublicationContent(ctx, base, filename)
		if err != nil {
			return nil, err
		}
		after, err := p.nativePublicationContent(ctx, pull.GetHead().GetSHA(), filename)
		if err != nil {
			return nil, err
		}
		documents[filename] = [2]string{before, after}
		changes, err := nativeChanges(ctx, p.nativePublicationRead, filename, before, after)
		if err != nil {
			return nil, fmt.Errorf("derive source changes for %s: %w", filename, err)
		}
		intents = append(intents, changes...)
	}
	if len(intents) == 0 {
		return nil, nativeReject("no verifiable dependency version upgrade")
	}
	ecosystem := intents[0]["ecosystem"].(string)
	for _, item := range intents {
		if item["ecosystem"] != ecosystem {
			return nil, nativeReject("mixed dependency ecosystems")
		}
	}
	if len(ancillary) != 0 && ecosystem != "go" {
		return nil, nativeReject("mixed dependency ecosystems")
	}
	for _, filename := range ancillary {
		for _, sha := range []string{base, pull.GetHead().GetSHA()} {
			text, err := p.nativePublicationContent(ctx, sha, filename)
			if err != nil {
				return nil, err
			}
			for _, line := range nativeLines(text) {
				if !nativePublicationChecksum.MatchString(line) {
					return nil, nativeReject("malformed Go checksum evidence")
				}
			}
		}
	}
	return p.nativePublicationGroupIntent(ctx, pull, ecosystem, intents, documents)
}

// NativePublicationGroupIntent preserves primary consumer floors and separates regenerated npm solver output.
func (p *NativePublisher) nativePublicationGroupIntent(ctx context.Context, pull *github.PullRequest, ecosystem string, intents []map[string]any, documents map[string][2]string) (map[string]any, error) {
	primary := make([]map[string]any, 0, len(intents))
	locks := map[string]bool{}
	for _, item := range intents {
		filename := item["manifest"].(string)
		if nativeLockPath(filename) {
			locks[filename] = true
		}
		if item["direct"] == true && !nativeLockPath(filename) && !strings.HasSuffix(filename, ".lock.yml") {
			primary = append(primary, item)
		}
	}
	lockPaths := slices.AppendSeq(make([]string, 0, len(locks)), maps.Keys(locks))
	slices.Sort(lockPaths)
	if ecosystem == "npm" {
		if len(primary) == 0 {
			for _, item := range intents {
				if item["old"] == nil || item["target"] == nil {
					return nil, nativeReject("lock-only graph additions/removals lack primary upgrade evidence")
				}
			}
		}
		if len(primary) != 0 {
			roots := map[string]bool{}
			for _, item := range primary {
				root := path.Dir(item["manifest"].(string))
				roots[root] = true
				matching := make([]any, 0)
				for _, filename := range lockPaths {
					if path.Dir(filename) == root {
						matching = append(matching, filename)
					}
				}
				item["lock_manifests"] = matching
			}
			for _, filename := range lockPaths {
				if !roots[path.Dir(filename)] {
					return nil, nativeReject("native lock graph belongs to an unrelated manifest consumer")
				}
			}
			intents = primary
		}
	}
	selected := intents
	if len(primary) != 0 {
		selected = primary
	}
	family := nativeUpgradeFamily(ecosystem, selected[0]["package"].(string))
	for _, item := range selected {
		if nativeUpgradeFamily(ecosystem, item["package"].(string)) != family {
			return nil, nativeReject("multiple upgrade families require manual diagnosis")
		}
	}
	if ecosystem == "npm" && len(primary) != 0 {
		var err error
		intents, err = nativePublicationNPMConsumers(intents, primary, lockPaths, documents, family)
		if err != nil {
			return nil, fmt.Errorf("verify primary npm graph: %w", err)
		}
	}
	targets := make(map[string]any, len(selected))
	for _, item := range selected {
		pkg := item["package"].(string)
		if target, exists := targets[pkg]; exists && !reflect.DeepEqual(target, item["target"]) {
			return nil, nativeReject("conflicting native dependency targets")
		}
		targets[pkg] = item["target"]
	}
	target := nativePublicationTarget(targets)
	digest := sha256.Sum256([]byte(ecosystem + ":" + family))
	if ecosystem == "actions" {
		packages := slices.AppendSeq(make([]string, 0, len(targets)), maps.Keys(targets))
		slices.Sort(packages)
		for _, pkg := range packages {
			pin, ok := targets[pkg].(string)
			if !ok || !nativeSHA.MatchString(pin) {
				return nil, nativeReject("Actions target is not immutable")
			}
			if err := p.nativePublicationOfficialPin(ctx, nativeUpgradeFamily("actions", pkg), pin); err != nil {
				return nil, err
			}
		}
	}
	values := make([]any, len(intents))
	for index, item := range intents {
		values[index] = item
	}
	return map[string]any{
		"group": fmt.Sprintf("%x", digest[:12]), "ecosystem": ecosystem, "family": family,
		"target": target, "intents": values, "number": json.Number(strconv.Itoa(pull.GetNumber())),
		"head_sha": pull.GetHead().GetSHA(),
	}, nil
}

// NativePublicationNPMConsumers retains changed same-family root resolutions beyond explicit manifest bumps.
func nativePublicationNPMConsumers(intents, primary []map[string]any, lockPaths []string, documents map[string][2]string, family string) ([]map[string]any, error) {
	primaryPackages := map[string]bool{}
	for _, item := range primary {
		primaryPackages[item["package"].(string)] = true
	}
	for _, filename := range lockPaths {
		manifest := path.Join(path.Dir(filename), "package.json")
		texts, exists := documents[manifest]
		if !exists {
			return nil, nativeReject("native lock lacks immutable root manifest evidence")
		}
		root, err := nativeManifestVersions(manifest, texts[1])
		if err != nil {
			return nil, fmt.Errorf("read npm root manifest: %w", err)
		}
		var graphs [2]map[string]nativeResolved
		lockTexts, exists := documents[filename]
		if !exists {
			return nil, nativeReject("native lock lacks immutable graph evidence")
		}
		for index, text := range lockTexts {
			value, err := nativeDecode([]byte(text))
			if err != nil {
				return nil, fmt.Errorf("decode native npm lock: %w", err)
			}
			document, err := nativeObject(value)
			if err != nil {
				return nil, fmt.Errorf("read native npm lock: %w", err)
			}
			graphs[index], err = nativeLockVersions(document)
			if err != nil {
				return nil, fmt.Errorf("resolve native npm graph: %w", err)
			}
		}
		packages := map[string]bool{}
		for _, item := range root {
			key := item.Key.([]string)
			if len(key) == 2 && key[0] != "overrides" && !primaryPackages[key[1]] {
				packages[key[1]] = true
			}
		}
		ordered := slices.AppendSeq(make([]string, 0, len(packages)), maps.Keys(packages))
		slices.Sort(ordered)
		for _, pkg := range ordered {
			key := "node_modules/" + pkg
			before, oldOK := graphs[0][key]
			after, newOK := graphs[1][key]
			if oldOK && newOK && before.Version != after.Version && nativeUpgradeFamily("npm", pkg) == family {
				intents = append(intents, map[string]any{
					"ecosystem": "npm", "package": pkg, "old": before.Version, "target": after.Version,
					"manifest": filename, "direct": false, "key": key,
				})
			}
		}
	}
	return intents, nil
}

// NativePublicationTarget retains Python's sorted, spaced JSON formatting for mixed package targets.
func nativePublicationTarget(targets map[string]any) any {
	var single any
	first, equal := true, true
	for _, target := range targets {
		if first {
			single, first = target, false
		}
		equal = equal && reflect.DeepEqual(single, target)
	}
	if equal {
		return single
	}
	packages := slices.AppendSeq(make([]string, 0, len(targets)), maps.Keys(targets))
	slices.Sort(packages)
	var result strings.Builder
	result.WriteByte('{')
	for index, pkg := range packages {
		if index != 0 {
			result.WriteString(", ")
		}
		result.WriteString(nativePublicationJSONString(pkg))
		result.WriteString(": ")
		if targets[pkg] == nil {
			result.WriteString("null")
			continue
		}
		result.WriteString(nativePublicationJSONString(targets[pkg].(string)))
	}
	result.WriteByte('}')
	return result.String()
}

// NativePublicationJSONString matches Python's ASCII JSON strings without HTML escaping.
func nativePublicationJSONString(value string) string {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	var result strings.Builder
	for _, r := range strings.TrimSuffix(encoded.String(), "\n") {
		if r <= 127 {
			result.WriteRune(r)
			continue
		}
		if r <= 0xffff {
			fmt.Fprintf(&result, `\u%04x`, r)
			continue
		}
		high, low := utf16.EncodeRune(r)
		fmt.Fprintf(&result, `\u%04x\u%04x`, high, low)
	}
	return result.String()
}

// SourceGroup authorizes every selected source and proves complete same-family target membership.
func (p *NativePublisher) sourceGroup(ctx context.Context, sources []nativePublicationSource) (map[string]any, error) {
	if len(sources) == 0 || len(sources) > 100 {
		return nil, nativeReject("missing source PRs")
	}
	intents := make([]any, 0, len(sources))
	previous := 0
	for _, source := range sources {
		if source.Number <= previous || !nativeSHA.MatchString(source.HeadSHA) {
			return nil, nativeReject("invalid or unsorted source identity")
		}
		previous = source.Number
		snapshot, err := p.nativePublicationSnapshot(ctx, source.Number, source.HeadSHA, nativePublicationBot)
		if err != nil {
			return nil, fmt.Errorf("authorize source #%d: %w", source.Number, err)
		}
		if snapshot.blocked {
			return nil, nativeReject("human/unknown native hold blocks publication")
		}
		intent, err := p.nativePublicationSourceIntent(ctx, snapshot)
		if err != nil {
			return nil, fmt.Errorf("derive source #%d: %w", source.Number, err)
		}
		if len(intents) != 0 {
			first := intents[0].(map[string]any)
			if first["group"] != intent["group"] || !reflect.DeepEqual(first["target"], intent["target"]) {
				return nil, nativeReject("sources do not share one actual upgrade family/target")
			}
		}
		intents = append(intents, intent)
	}
	current, err := p.nativePublicationInventory(ctx, nativePublicationBot)
	if err != nil {
		return nil, err
	}
	if err := p.nativePublicationSiblings(ctx, intents, current); err != nil {
		return nil, fmt.Errorf("authorize complete source family: %w", err)
	}
	first := intents[0].(map[string]any)
	return map[string]any{"group": first["group"], "ecosystem": first["ecosystem"], "family": first["family"], "target": first["target"], "sources": intents}, nil
}

// NativePublicationSiblingEcosystem recognizes only unchanged-path modified files as disjointness proof.
func nativePublicationSiblingEcosystem(file *github.CommitFile) string {
	if file == nil || !safeNativePath(file.GetFilename()) || file.GetStatus() != "modified" || file.GetPreviousFilename() != "" {
		return ""
	}
	switch path.Base(file.GetFilename()) {
	case "go.sum", "go.work.sum":
		return "go"
	case "Dockerfile":
		return "docker"
	}
	return nativeEcosystem(file.GetFilename())
}

// NativePublicationSiblings fails closed unless every omitted source has complete proof of different intent.
func (p *NativePublisher) nativePublicationSiblings(ctx context.Context, intents []any, current []*github.PullRequest) error {
	selected := map[int]string{}
	for _, value := range intents {
		intent := value.(map[string]any)
		number, _ := nativeInteger(intent["number"])
		selected[int(number)] = intent["head_sha"].(string)
	}
	first := intents[0].(map[string]any)
	for _, pull := range current {
		if expected, exists := selected[pull.GetNumber()]; exists {
			if pull.GetHead().GetSHA() != expected {
				return nativeReject("native source head changed during authorization")
			}
			delete(selected, pull.GetNumber())
			continue
		}
		snapshot, err := p.nativePublicationSnapshot(ctx, pull.GetNumber(), pull.GetHead().GetSHA(), nativePublicationBot)
		if err != nil {
			return fmt.Errorf("PR #%d sibling snapshot: %w", pull.GetNumber(), err)
		}
		unknown, overlapping := false, false
		for _, file := range snapshot.files {
			ecosystem := nativePublicationSiblingEcosystem(file)
			unknown = unknown || ecosystem == ""
			overlapping = overlapping || ecosystem == first["ecosystem"]
		}
		if len(snapshot.files) != 0 && !unknown && !overlapping {
			continue
		}
		candidate, err := p.nativePublicationSourceIntent(ctx, snapshot)
		if err != nil {
			return fmt.Errorf("PR #%d sibling intent: %w", pull.GetNumber(), err)
		}
		if candidate["group"] == first["group"] && reflect.DeepEqual(candidate["target"], first["target"]) {
			return nativeReject(fmt.Sprintf("replacement omits current same-family/target native source #%d", pull.GetNumber()))
		}
	}
	if len(selected) != 0 {
		return nativeReject("native source inventory changed during authorization")
	}
	return nil
}
