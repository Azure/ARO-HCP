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

// Package discovery reads repository pins and, when enabled, upstream releases.
//
// Local pin discovery needs no credentials. A failed lookup is recorded as an
// error or a blocked family; it is never reported as an up-to-date version.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/repository"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/semver"
)

// ProfileTools is the only profile this build implements.
const ProfileTools = "tools"

// Options controls discovery.
type Options struct {
	// Offline restricts discovery to tracked pins and performs no lookups.
	Offline bool
	// Resolver performs upstream lookups. It is ignored when Offline is set.
	Resolver Resolver
	// Clock supplies the observation timestamp.
	Clock func() time.Time
}

// Resolver resolves the newest eligible upstream release for a family.
type Resolver interface {
	// LatestRelease returns the newest stable release tag for a GitHub repository.
	LatestRelease(ctx context.Context, repository string) (string, error)
	// LatestModule returns the newest stable module version for a Go module path.
	LatestModule(ctx context.Context, modulePath string) (string, error)
}

// ErrOffline reports a lookup requested while discovery is offline.
var ErrOffline = errors.New("offline discovery cannot resolve upstream releases")

// Build produces the inventory for a checkout at an immutable revision.
func Build(ctx context.Context, checkout *repository.Repo, revision, profile string, options Options) (model.Inventory, error) {
	if profile != ProfileTools {
		return model.Inventory{}, fmt.Errorf("unsupported profile %q", profile)
	}
	if !repository.IsCommit(revision) {
		return model.Inventory{}, fmt.Errorf("revision must be a full commit SHA, got %q", revision)
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	scope := model.ScopeRemoteResolved
	if options.Offline {
		scope = model.ScopeLocalOnly
	}
	inventory := model.Inventory{
		Envelope:      schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindInventory},
		Repository:    repositoryName(ctx, checkout),
		BaseSHA:       revision,
		Profile:       profile,
		EvidenceScope: scope,
		ObservedAt:    clock().UTC().Format(time.RFC3339),
	}
	collector := &collector{families: map[string]*model.Family{}}
	collector.collectCIPins(ctx, filepath.Join(checkout.Path))
	collector.collectBingoPins(ctx, filepath.Join(checkout.Path))
	inventory.Errors = collector.errors
	inventory.Families = collector.rows()

	resolve := !options.Offline
	if resolve && options.Resolver == nil {
		inventory.Errors = append(inventory.Errors, "online discovery requested without a release resolver")
		resolve = false
	}
	for index := range inventory.Families {
		family := &inventory.Families[index]
		if family.Status == model.VersionError {
			continue
		}
		if !resolve {
			family.Status = model.VersionBlocked
			family.Reason = "offline inventory: upstream release not resolved"
			continue
		}
		latest, err := resolveFamily(ctx, options.Resolver, *family)
		if err != nil {
			family.Status = model.VersionError
			family.Reason = err.Error()
			inventory.Errors = append(inventory.Errors, family.Family+": "+err.Error())
			continue
		}
		family.Latest = latest
		family.Status, family.Reason = versionState(family.Current, latest, len(family.Pins))
	}
	inventory.Complete = len(inventory.Errors) == 0
	for _, family := range inventory.Families {
		if family.Status == model.VersionError {
			inventory.Complete = false
		}
	}
	return inventory, nil
}

// Returns OWNER/REPO from the checkout remotes, or an empty string.
func repositoryName(ctx context.Context, checkout *repository.Repo) string {
	name, err := checkout.TopLevel(ctx)
	if err != nil {
		return ""
	}
	return name
}

// Resolves the newest eligible release for one family.
func resolveFamily(ctx context.Context, resolver Resolver, family model.Family) (string, error) {
	var latest string
	var err error
	switch family.Kind {
	case model.FamilyGitHubRelease:
		latest, err = resolver.LatestRelease(ctx, family.Family)
	case model.FamilyGoModule:
		latest, err = resolver.LatestModule(ctx, family.Family)
	default:
		return "", fmt.Errorf("unsupported family kind %q", family.Kind)
	}
	if err != nil {
		return "", err
	}
	if _, err := semver.Parse(latest); err != nil {
		return "", fmt.Errorf("upstream version %q is not semantic: %w", latest, err)
	}
	return latest, nil
}

// Classifies a family from its pins and the resolved release.
func versionState(current, latest string, pinCount int) (string, string) {
	if pinCount != 1 {
		return model.VersionBlocked, "family pins disagree; see per-tool current versions"
	}
	latestVersion, err := semver.Parse(latest)
	if err != nil {
		return model.VersionError, err.Error()
	}
	if !latestVersion.Stable {
		return model.VersionBlocked, "upstream latest is not a stable release"
	}
	currentValue, err := semver.Parse(current)
	if err != nil {
		return model.VersionError, err.Error()
	}
	switch semver.Compare(currentValue, latestVersion) {
	case -1:
		return model.VersionOutdated, ""
	case 1:
		return model.VersionAhead, ""
	default:
		return model.VersionCurrent, ""
	}
}

type collector struct {
	families map[string]*model.Family
	errors   []string
}

// Adds one pin to its family row.
func (c *collector) addPin(family, kind, sourceURL string, pin model.Pin) {
	row, ok := c.families[family]
	if !ok {
		row = &model.Family{Family: family, Kind: kind, SourceURL: sourceURL, Status: model.VersionError}
		c.families[family] = row
	}
	row.Pins = append(row.Pins, pin)
}

// Reads dev-infrastructure/openshift-ci pins.
//
// The variable-to-upstream mapping comes from the repository's own script so a
// new pinned tool needs no depctl change.
// Reads dev-infrastructure/openshift-ci pins.
func (c *collector) collectCIPins(ctx context.Context, root string) {
	script := filepath.Join(root, "dev-infrastructure", "openshift-ci", "update-tool-versions.sh")
	versionsPath := filepath.Join(root, "dev-infrastructure", "openshift-ci", "versions.mk")
	metadata, err := toolMetadata(ctx, script)
	if err != nil {
		c.errors = append(c.errors, "CI pin discovery: "+err.Error())
		return
	}
	if len(metadata) == 0 {
		c.errors = append(c.errors, "CI pin discovery: empty tool mapping")
		return
	}
	versions, err := os.ReadFile(versionsPath)
	if err != nil {
		c.errors = append(c.errors, "CI pin discovery: "+err.Error())
		return
	}
	for _, tool := range metadata {
		if tool.Variable == "" || tool.Repository == "" {
			c.errors = append(c.errors, "CI pin discovery: incomplete tool mapping entry")
			continue
		}
		current, ok := makeVariable(string(versions), tool.Variable)
		pin := model.Pin{
			ToolName: strings.ToLower(strings.TrimSuffix(tool.Variable, "_VERSION")),
			Source:   "dev-infrastructure/openshift-ci/versions.mk",
			Variable: tool.Variable,
			Current:  current,
		}
		if !ok {
			pin.Error = "variable is not assigned exactly once"
		}
		c.addPin(tool.Repository, model.FamilyGitHubRelease,
			"https://api.github.com/repos/"+tool.Repository+"/releases/latest", pin)
	}
}

type toolMapping struct {
	Variable   string `json:"variable"`
	Repository string `json:"repository"`
}

// Reads the tool-to-repository mapping from the repository script.
func toolMetadata(ctx context.Context, script string) ([]toolMapping, error) {
	runContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(runContext, "bash", script, "--metadata-json")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%s --metadata-json failed: %w: %s", script, err, strings.TrimSpace(stderr.String()))
	}
	var metadata []toolMapping
	if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
		return nil, fmt.Errorf("decode %s metadata: %w", script, err)
	}
	return metadata, nil
}

var assignmentPattern = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)\s*\?=\s*(\S+)\s*$`)

// Returns the single assignment of a make variable.
func makeVariable(content, name string) (string, bool) {
	var found []string
	for _, match := range assignmentPattern.FindAllStringSubmatch(content, -1) {
		if match[1] == name {
			found = append(found, match[2])
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// Reads the pinned tool module files under .bingo.
func (c *collector) collectBingoPins(ctx context.Context, root string) {
	directory := filepath.Join(root, ".bingo")
	entries, err := os.ReadDir(directory)
	if err != nil {
		c.errors = append(c.errors, "Bingo pin discovery: "+err.Error())
		return
	}
	var modules []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mod") || entry.Name() == "go.mod" {
			continue
		}
		modules = append(modules, entry.Name())
	}
	if len(modules) == 0 {
		c.errors = append(c.errors, "Bingo pin discovery: no tool module files found")
		return
	}
	sort.Strings(modules)
	for _, name := range modules {
		tool := strings.TrimSuffix(name, ".mod")
		source := ".bingo/" + name
		modulePath, version, err := bingoRequirement(ctx, directory, name)
		if err != nil {
			c.addPin("pin:"+source, model.FamilyGoModule, "", model.Pin{
				ToolName: tool, Source: source, Error: err.Error(),
			})
			continue
		}
		c.addPin(modulePath, model.FamilyGoModule, "https://pkg.go.dev/"+modulePath, model.Pin{
			ToolName: tool, Source: source, Current: version,
		})
	}
}

type moduleFile struct {
	Require []struct {
		Path     string `json:"Path"`
		Version  string `json:"Version"`
		Indirect bool   `json:"Indirect"`
	} `json:"Require"`
	Replace []json.RawMessage `json:"Replace"`
	Exclude []json.RawMessage `json:"Exclude"`
}

// Reads one tool module with native Go tooling.
func bingoRequirement(ctx context.Context, directory, name string) (string, string, error) {
	runContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(runContext, "go", "mod", "edit", "-json", "-modfile="+name)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", "", fmt.Errorf("go mod edit failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var parsed moduleFile
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return "", "", fmt.Errorf("decode module file: %w", err)
	}
	var direct []struct {
		Path    string
		Version string
	}
	for _, requirement := range parsed.Require {
		if !requirement.Indirect {
			direct = append(direct, struct {
				Path    string
				Version string
			}{requirement.Path, requirement.Version})
		}
	}
	if len(direct) != 1 {
		return "", "", fmt.Errorf("expected one direct upstream requirement, found %d", len(direct))
	}
	if len(parsed.Replace) > 0 || len(parsed.Exclude) > 0 {
		return "", "", errors.New("replace or exclude directives are unsupported")
	}
	if direct[0].Path == "" || direct[0].Version == "" {
		return "", "", errors.New("missing upstream module path or version")
	}
	return direct[0].Path, direct[0].Version, nil
}

// Finalizes family rows in a stable order.
func (c *collector) rows() []model.Family {
	var families []*model.Family
	for _, family := range c.families {
		families = append(families, family)
	}
	sort.Slice(families, func(left, right int) bool { return families[left].Family < families[right].Family })
	rows := make([]model.Family, 0, len(families))
	for _, family := range families {
		sort.Slice(family.Pins, func(left, right int) bool {
			return family.Pins[left].ToolName < family.Pins[right].ToolName
		})
		names := make([]string, 0, len(family.Pins))
		currents := map[string]bool{}
		failed := false
		for _, pin := range family.Pins {
			names = append(names, pin.ToolName)
			if pin.Error != "" {
				failed = true
				continue
			}
			currents[pin.Current] = true
		}
		family.ToolNames = names
		if len(currents) == 1 {
			for value := range currents {
				family.Current = value
			}
		}
		switch {
		case failed:
			family.Status = model.VersionError
			family.Reason = "one or more pins could not be read"
		case len(currents) != 1:
			family.Status = model.VersionBlocked
			family.Reason = "family pins disagree; see per-tool current versions"
		default:
			family.Status = model.VersionBlocked
		}
		rows = append(rows, *family)
	}
	return rows
}
