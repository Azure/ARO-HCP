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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
)

const nativeMaxPatch = 16 * 1024 * 1024
const nativeMaxRequest = 6*nativeMaxPatch + 256*1024
const nativeRequestFile = "native-replacement-request.json"

// NativePrepareOptions identifies the exact offline selection and output files.
type NativePrepareOptions struct {
	Repository string
	Root       string
	Inventory  string
	Group      string
	Target     string
	TitleFile  string
	BodyFile   string
	Artifacts  string
}

var nativeTextDirective = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#\p{Nd}+`)
var nativePatchHeader = regexp.MustCompile(`(?m)^diff --git a/(\S+) b/(\S+)$`)
var nativePatchHunk = regexp.MustCompile(`(?m)^@@ -[0-9]+(?:,[0-9]+)? \+[0-9]+(?:,[0-9]+)? @@`)
var nativePatchMode = regexp.MustCompile(`(?m)^(?:new|deleted) file mode ([0-9]+)$`)
var nativeForbiddenPatch = regexp.MustCompile(`(?m)^(?:old mode|new mode|rename |copy |GIT binary patch|Binary files)`)

// Native safe text normalizes every notification delimiter without touching patches.
func nativeSafeText(text string, title bool) (string, error) {
	text = strings.ReplaceAll(text, "@", "＠")
	limit := 20000
	if title {
		limit = 200
	}
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) == 0 || utf8.RuneCountInString(text) > limit || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("invalid publication text")
	}
	for _, r := range text {
		if r < 9 || r == 11 || r == 12 || r >= 14 && r <= 31 || r == 127 {
			return "", fmt.Errorf("invalid publication text")
		}
	}
	if nativeHasTextDirective(text) || strings.Contains(text, "native-dependabot-maintenance:") || strings.Contains(text, "<!-- gh-aw-workflow-id:") {
		return "", fmt.Errorf("publication text cannot contain commands, auto-close directives or ownership markers")
	}
	if title && (!strings.HasPrefix(text, "chore(deps): ") || strings.TrimSpace(strings.TrimPrefix(text, "chore(deps): ")) == "" || strings.ContainsAny(text, "\r\n")) {
		return "", fmt.Errorf("replacement title requires a single-line chore(deps) summary")
	}
	return text, nil
}

// Native text directives retain Unicode whitespace and word-boundary semantics.
func nativeHasTextDirective(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "/") {
			return true
		}
	}
	normalized := strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return ' '
		}
		return character
	}, text)
	for _, match := range nativeTextDirective.FindAllStringIndex(normalized, -1) {
		if match[0] == 0 {
			return true
		}
		previous, _ := utf8.DecodeLastRuneInString(normalized[:match[0]])
		if previous != '_' && !unicode.IsLetter(previous) && !unicode.IsNumber(previous) {
			return true
		}
	}
	return false
}

// Native patch paths reject ambiguous headers, special files and binary changes.
func nativePatchPaths(patch string) ([]string, error) {
	if len(patch) == 0 || len(patch) > nativeMaxPatch || !utf8.ValidString(patch) || strings.ContainsRune(patch, 0) {
		return nil, fmt.Errorf("patch must be nonempty UTF-8 and at most 16 MiB")
	}
	headers := nativePatchHeader.FindAllStringSubmatch(patch, -1)
	if len(headers) == 0 || !strings.HasPrefix(patch, "diff --git ") || len(headers) != strings.Count(patch, "\ndiff --git ")+1 || !nativePatchHunk.MatchString(patch) || nativeForbiddenPatch.MatchString(patch) {
		return nil, fmt.Errorf("malformed or forbidden patch")
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(headers))
	for _, header := range headers {
		if header[1] != header[2] || !safeNativePath(header[1]) || seen[header[1]] {
			return nil, fmt.Errorf("renamed, duplicate or unsafe patch path")
		}
		seen[header[1]] = true
		paths = append(paths, header[1])
	}
	for _, mode := range nativePatchMode.FindAllStringSubmatch(patch, -1) {
		if mode[1] != "100644" {
			return nil, fmt.Errorf("only regular patch files are allowed")
		}
	}
	return paths, nil
}

// PrepareNativeRequest constructs a deterministic digest-bound request entirely offline.
func PrepareNativeRequest(ctx context.Context, options NativePrepareOptions) (map[string]string, error) {
	inventory, err := os.Open(options.Inventory)
	if err != nil {
		return nil, err
	}
	defer inventory.Close()
	selected, err := SelectNativeInventoryGroup(inventory, options.Repository, options.Group, options.Target)
	if err != nil {
		return nil, err
	}
	var group nativeInventoryGroup
	if err := json.Unmarshal(selected.Group, &group); err != nil {
		return nil, err
	}
	repo, err := repository.Open(options.Root)
	if err != nil {
		return nil, err
	}
	patch, err := repo.NativeRequestPatch(ctx, selected.BaseSHA)
	if err != nil {
		return nil, err
	}
	title, err := os.ReadFile(options.TitleFile)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(options.BodyFile)
	if err != nil {
		return nil, err
	}
	safeTitle, err := nativeSafeText(strings.TrimRight(string(title), "\r\n"), true)
	if err != nil {
		return nil, err
	}
	safeBody, err := nativeSafeText(string(body), false)
	if err != nil {
		return nil, err
	}
	sources := make([]map[string]any, 0, len(group.Sources))
	for _, source := range group.Sources {
		sources = append(sources, map[string]any{"number": json.Number(string(source.Number)), "head_sha": source.HeadSHA})
	}
	paths, err := nativePatchPaths(string(patch))
	if err != nil {
		return nil, err
	}
	if err := nativeValidateReplacementPaths(paths, group); err != nil {
		return nil, err
	}
	request := map[string]any{"source_prs": sources, "expected_base_sha": selected.BaseSHA, "patch": string(patch), "title": safeTitle, "body": safeBody}
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(request); err != nil {
		return nil, err
	}
	data := bytes.TrimSuffix(payload.Bytes(), []byte("\n"))
	if len(data) > nativeMaxRequest {
		return nil, fmt.Errorf("replacement request artifact exceeds size limit")
	}
	if err := nativeWriteRequest(options.Artifacts, data); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	return map[string]string{"request_sha256": hex.EncodeToString(digest[:])}, nil
}

// Native request writes never follow symlinks or truncate multiply linked files.
func nativeWriteRequest(directory string, payload []byte) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	fd, err := unix.Open(filepath.Clean(directory), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	fileFD, err := unix.Openat(fd, nativeRequestFile, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fileFD), nativeRequestFile)
	defer file.Close()
	var info unix.Stat_t
	if err := unix.Fstat(fileFD, &info); err != nil {
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
		return fmt.Errorf("replacement request destination must be a singly linked regular file")
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if n, err := file.Write(payload); err != nil {
		return err
	} else if n != len(payload) {
		return fmt.Errorf("short request artifact write")
	}
	return nil
}

var nativeSDKConsumer = regexp.MustCompile(`^test/sdk/v[0-9]+(?:preview)?/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/.+$`)

// NativeValidateReplacementPaths limits packaging to local dependency consumers.
func nativeValidateReplacementPaths(paths []string, group nativeInventoryGroup) error {
	for _, filename := range paths {
		name := path.Base(filename)
		allowed := false
		switch group.Ecosystem {
		case "go":
			allowed = name == "go.mod" || name == "go.sum" || name == "go.work.sum" || strings.HasPrefix(filename, "vendor/") || strings.HasPrefix(filename, "image-sync/oc-mirror/acrauth/vendor/")
		case "npm":
			allowed = name == "package.json" || name == "package-lock.json" || name == "npm-shrinkwrap.json"
			if group.Family == "typespec" {
				allowed = allowed || strings.HasPrefix(filename, "api/redhatopenshift/") && (strings.HasSuffix(filename, ".tsp") || strings.HasSuffix(filename, "/openapi.json")) || strings.HasPrefix(filename, "internal/azureapi/") && strings.Contains(filename, "/generated/") && strings.HasSuffix(filename, ".go") || nativeSDKConsumer.MatchString(filename) && (strings.HasSuffix(filename, ".go") || strings.HasSuffix(filename, "/testdata/_metadata.json") || name == "go.mod" || name == "go.sum" || name == "LICENSE" || name == "LICENSE.txt")
			}
		case "actions":
			allowed = strings.HasPrefix(filename, ".github/workflows/") && (strings.HasSuffix(filename, ".md") || strings.HasSuffix(filename, ".yml") || strings.HasSuffix(filename, ".yaml")) || filename == ".github/aw/actions-lock.json"
		}
		if !allowed {
			return fmt.Errorf("protected or unrelated replacement path: %s", filename)
		}
	}
	return nil
}
