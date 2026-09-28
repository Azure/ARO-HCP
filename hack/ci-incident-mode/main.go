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

// ci-incident-mode opens a draft release PR using a temporary upstream checkout.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const (
	pipelinePath   = "core-services/pipeline-controller/config.yaml"
	retesterPath   = "core-services/retester/_config.yaml"
	pipelineAnchor = "- org: Azure\n  repos:\n    - name: ARO-HCP\n      branches:\n        - main\n      mode:\n        trigger: "
	retesterAnchor = "    Azure:\n      enabled: true\n      repos:\n        ARO-HCP:\n          enabled: "
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "enable" && os.Args[1] != "disable") {
		fmt.Fprintln(os.Stderr, "usage: ci-incident-mode <enable|disable>")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := createPR(ctx, os.Args[1], run); err != nil {
		fmt.Fprintln(os.Stderr, "Cannot create incident-mode PR:", err)
		os.Exit(1)
	}
}

// decodeConfig rejects duplicate keys and multiple YAML documents rather than
// letting an ambiguous config pass the semantic and exact-layout checks.
func decodeConfig(contents []byte) (map[string]any, *yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	var config map[string]any
	if err := document.Decode(&config); err != nil {
		return nil, nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("expected a single YAML document")
		}
		return nil, nil, err
	}
	return config, document.Content[0], nil
}

func objectAt(config map[string]any, keys ...string) (map[string]any, error) {
	for _, key := range keys {
		next, ok := config[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("missing or invalid mapping %q", key)
		}
		config = next
	}
	return config, nil
}

func validateTargets(pipeline, retester []byte) (*yaml.Node, *yaml.Node, error) {
	config, pipelineRoot, err := decodeConfig(pipeline)
	if err != nil {
		return nil, nil, fmt.Errorf("pipeline YAML: %w", err)
	}
	orgs, ok := config["orgs"].([]any)
	orgNodes := nodeAt(pipelineRoot, "orgs")
	if !ok || orgNodes == nil || orgNodes.Kind != yaml.SequenceNode {
		return nil, nil, errors.New("expected pipeline organizations in the supported YAML layout")
	}
	var azure map[string]any
	var azureNode *yaml.Node
	for index, value := range orgs {
		org, ok := value.(map[string]any)
		if !ok {
			return nil, nil, errors.New("expected a pipeline organization mapping")
		}
		if org["org"] == "Azure" {
			if azure != nil {
				return nil, nil, errors.New("duplicate Azure pipeline organization")
			}
			azure = org
			azureNode = orgNodes.Content[index]
		}
	}
	if azure == nil {
		return nil, nil, errors.New("missing Azure pipeline organization")
	}
	repos, ok := azure["repos"].([]any)
	repoNodes := nodeAt(azureNode, "repos")
	if !ok || repoNodes == nil || repoNodes.Kind != yaml.SequenceNode {
		return nil, nil, errors.New("expected Azure pipeline repositories in the supported YAML layout")
	}
	var target map[string]any
	var targetNode *yaml.Node
	for index, value := range repos {
		if shorthand, ok := value.(string); ok && shorthand == "ARO-HCP" {
			return nil, nil, errors.New("expected exactly one named ARO-HCP pipeline repository")
		}
		if repo, ok := value.(map[string]any); ok && repo["name"] == "ARO-HCP" {
			if target != nil {
				return nil, nil, errors.New("duplicate ARO-HCP pipeline repository")
			}
			target = repo
			targetNode = repoNodes.Content[index]
		}
	}
	branches, ok := target["branches"].([]any)
	if !ok || len(branches) != 1 || branches[0] != "main" {
		return nil, nil, errors.New("expected one ARO-HCP pipeline repository scoped to main only")
	}
	retries, retesterRoot, err := decodeConfig(retester)
	if err != nil {
		return nil, nil, fmt.Errorf("retester YAML: %w", err)
	}
	if _, err := objectAt(retries, "retester", "orgs", "Azure", "repos", "ARO-HCP"); err != nil {
		return nil, nil, err
	}
	return nodeAt(targetNode, "mode", "trigger"),
		nodeAt(retesterRoot, "retester", "orgs", "Azure", "repos", "ARO-HCP", "enabled"), nil
}

func nodeAt(node *yaml.Node, keys ...string) *yaml.Node {
	for _, key := range keys {
		if node == nil || node.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				next = node.Content[i+1]
				break
			}
		}
		node = next
	}
	return node
}

// replaceSetting binds the exact-layout check to the validated scalar's source
// position. A canonical-looking block elsewhere cannot select the edit location.
func replaceSetting(contents []byte, node *yaml.Node, anchor, current, desired string) ([]byte, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Value != current {
		return nil, fmt.Errorf("expected setting %s in the supported YAML layout", current)
	}
	offset := 0
	for line := 1; line < node.Line; line++ {
		end := bytes.IndexByte(contents[offset:], '\n')
		if end < 0 {
			return nil, errors.New("setting source line not found")
		}
		offset += end + 1
	}
	// The supported anchor requires an ASCII-only prefix on the scalar's line.
	offset += node.Column - 1
	start := offset - len(anchor)
	expected := []byte(anchor + current + "\n")
	if start < 0 || start > len(contents) || !bytes.HasPrefix(contents[start:], expected) {
		return nil, errors.New("validated setting does not use the supported YAML layout")
	}
	result := make([]byte, 0, len(contents)-len(current)+len(desired))
	result = append(result, contents[:offset]...)
	result = append(result, desired...)
	result = append(result, contents[offset+len(current):]...)
	return result, nil
}

func toggleContents(pipeline, retester []byte, action string) ([]byte, []byte, error) {
	current, desired, retriesCurrent, retriesDesired := "auto", "manual", "true", "false"
	switch action {
	case "enable":
	case "disable":
		current, desired, retriesCurrent, retriesDesired = "manual", "auto", "false", "true"
	default:
		return nil, nil, fmt.Errorf("unknown action %q", action)
	}
	pipelineNode, retesterNode, err := validateTargets(pipeline, retester)
	if err != nil {
		return nil, nil, err
	}
	pipeline, err = replaceSetting(pipeline, pipelineNode, pipelineAnchor, current, desired)
	if err != nil {
		return nil, nil, fmt.Errorf("pipeline: %w", err)
	}
	retester, err = replaceSetting(retester, retesterNode, retesterAnchor, retriesCurrent, retriesDesired)
	if err != nil {
		return nil, nil, fmt.Errorf("retester: %w", err)
	}
	return pipeline, retester, nil
}

func applyToggle(repository, action string) error {
	pipelineFile := filepath.Join(repository, pipelinePath)
	retesterFile := filepath.Join(repository, retesterPath)
	pipeline, err := os.ReadFile(pipelineFile)
	if err != nil {
		return err
	}
	retester, err := os.ReadFile(retesterFile)
	if err != nil {
		return err
	}
	pipeline, retester, err = toggleContents(pipeline, retester, action)
	if err != nil {
		return err
	}
	// Writes are sequential, but both must succeed before any commit or push.
	// A failed temporary checkout is discarded, never published partially.
	if err := os.WriteFile(retesterFile, retester, 0o644); err != nil {
		return err
	}
	return os.WriteFile(pipelineFile, pipeline, 0o644)
}

func run(ctx context.Context, directory, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, exit.Stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func createPR(ctx context.Context, action string, run func(context.Context, string, string, ...string) (string, error)) error {
	if _, err := run(ctx, "", "gh", "auth", "status", "--hostname", "github.com"); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "arohcp-ci-incident-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			fmt.Fprintf(os.Stderr, "Remove temporary checkout %s: %v\n", directory, err)
		}
	}()
	repository := filepath.Join(directory, "release")
	if _, err := run(ctx, "", "git", "clone", "--depth=1", "--single-branch", "--branch", "main",
		"https://github.com/openshift/release.git", repository); err != nil {
		return err
	}
	branch := "ci/" + filepath.Base(directory) + "-" + action
	if _, err := run(ctx, repository, "git", "switch", "-c", branch); err != nil {
		return err
	}
	if err := applyToggle(repository, action); err != nil {
		return err
	}
	if _, err := run(ctx, repository, "git", "add", "--", pipelinePath, retesterPath); err != nil {
		return err
	}
	title := "ci(aro-hcp): " + action + " CI incident mode"
	if _, err := run(ctx, repository, "git", "commit", "-m", title); err != nil {
		return err
	}
	// GitHub returns the existing fork, including its actual name if renamed.
	forkJSON, err := run(ctx, repository, "gh", "api", "--hostname", "github.com", "--method", "POST",
		"repos/openshift/release/forks")
	if err != nil {
		return err
	}
	var fork struct {
		CloneURL string `json:"clone_url"`
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal([]byte(forkJSON), &fork); err != nil {
		return err
	}
	if fork.CloneURL == "" || fork.FullName == "" || fork.Owner.Login == "" {
		return errors.New("GitHub did not return the fork URL and owner")
	}
	if _, err := run(ctx, repository, "git", "-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential",
		"push", fork.CloneURL, "HEAD:refs/heads/"+branch); err != nil {
		return err
	}
	head := fork.Owner.Login + ":" + branch
	fmt.Printf("Branch pushed to %s: %s\n", fork.FullName, branch)
	url, err := run(ctx, repository, "gh", "pr", "create", "--repo", "openshift/release",
		"--base", "main", "--head", head, "--draft", "--title", title, "--body", prBody(action))
	if err != nil {
		return fmt.Errorf("%w\nBranch is already pushed. Do not rerun the toggle; check for an existing PR first, then if absent run:\ngh pr create --repo openshift/release --base main --head %s --draft", err, head)
	}
	fmt.Println(url)
	return nil
}

func prBody(action string) string {
	mode, retests := "manual", "disabled"
	if action == "disable" {
		mode, retests = "auto", "enabled"
	}
	return fmt.Sprintf(`### What

Set Azure/ARO-HCP main pipeline scheduling to %s and repository-wide automated retests to %s.

### Why

Prepare the paired configuration change to %s CI incident mode. The incident owner must link the incident and confirm ownership of both settings before taking this draft through review. Retester scope includes all branches and jobs, including inexpensive checks.

### Testing

The helper validated both starting states and supported YAML layouts before editing only the two ARO-HCP settings. No live service configuration was changed or tested by opening this PR. Review the complete diff before approval.

### Operational requirements

This draft requires approval from the release configuration owners. After merge, confirm pipeline-controller loaded the mode and coordinate a platform-managed retester restart after its ConfigMap is updated. Do not assume merge is activation. Before recovery, confirm no other incident still needs either restriction. Running jobs, manual commands, and independent retry systems remain unaffected.
`, mode, retests, action)
}
