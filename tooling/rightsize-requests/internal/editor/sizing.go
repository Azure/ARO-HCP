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

package editor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type SizingEntry struct {
	Deployment, Container string
	CPU, Memory           string
}

type SizingUpdate struct {
	Deployment, Container, Resource, NewValue string
}

// SizingEditor edits only existing e2e_minimal requests in the limited branch.
type SizingEditor struct {
	path, original string
	entries        []SizingEntry
	targets        map[[3]string]*yaml.Node
}

var sizingAction = regexp.MustCompile(`^[ \t]*{{(?:-[ \t]+|[ \t]*)(if[ \t]+\.Values\.limitClusterSizes|else|end)(?:[ \t]+-|[ \t]*)}}[ \t]*\r?$`)
var sizingCPU = regexp.MustCompile(`^[1-9][0-9]*m$`)
var sizingMemory = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)

// NewSizing accepts one standalone if/else/end, not arbitrary Helm templates.
func NewSizing(path string) (*SizingEditor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	e := &SizingEditor{path: path, original: string(data), targets: map[[3]string]*yaml.Node{}}
	lines := strings.Split(e.original, "\n")
	branches := [2][]string{make([]string, len(lines)), make([]string, len(lines))}
	state := 0
	for i, line := range lines {
		if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
			match := sizingAction.FindStringSubmatch(line)
			if match == nil || state >= 3 || strings.Fields(match[1])[0] != []string{"if", "else", "end"}[state] {
				return nil, fmt.Errorf("unsupported sizing template action at line %d", i+1)
			}
			state++
			continue
		}
		if state == 0 || state == 3 {
			text := strings.TrimSpace(line)
			if text != "" && !strings.HasPrefix(text, "#") && (state != 0 || text != "---") {
				return nil, fmt.Errorf("content outside sizing conditional at line %d", i+1)
			}
			branches[0][i], branches[1][i] = line, line
		} else {
			branches[state-1][i] = line
		}
	}
	if state != 3 {
		return nil, fmt.Errorf("expected sizing if/else/end")
	}
	for branch, text := range branches {
		var root, extra yaml.Node
		decoder := yaml.NewDecoder(strings.NewReader(strings.Join(text, "\n")))
		if err := decoder.Decode(&root); err != nil {
			return nil, fmt.Errorf("sizing branch %d: %w", branch, err)
		}
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("unexpected sizing document or trailing syntax: %v", err)
		}
		if err := validateSizingNode(&root, lines); err != nil {
			return nil, err
		}
		if sizingString(findChild(&root, "apiVersion")) != "scheduling.hypershift.openshift.io/v1alpha1" ||
			sizingString(findChild(&root, "kind")) != "ClusterSizingConfiguration" ||
			sizingString(sizingChild(findChild(&root, "metadata"), "name")) != "cluster" {
			return nil, fmt.Errorf("expected ClusterSizingConfiguration cluster")
		}
		sizes := sizingChild(findChild(&root, "spec"), "sizes")
		if sizes == nil || sizes.Kind != yaml.SequenceNode || len(sizes.Content) == 0 {
			return nil, fmt.Errorf("expected nonempty spec.sizes list")
		}
		names := map[string]bool{}
		for _, size := range sizes.Content {
			name := sizingString(sizingChild(size, "name"))
			if name == "" || names[name] {
				return nil, fmt.Errorf("missing or duplicate size name %q", name)
			}
			names[name] = true
			requests := sizingChild(sizingChild(size, "effects"), "resourceRequests")
			if requests == nil || requests.Kind != yaml.SequenceNode || len(requests.Content) == 0 {
				return nil, fmt.Errorf("expected resourceRequests list for %s", name)
			}
			seen := map[[2]string]bool{}
			for _, request := range requests.Content {
				entry := SizingEntry{Deployment: sizingString(sizingChild(request, "deploymentName")), Container: sizingString(sizingChild(request, "containerName"))}
				key := [2]string{entry.Deployment, entry.Container}
				if key[0] == "" || key[1] == "" || seen[key] {
					return nil, fmt.Errorf("missing or duplicate deployment/container in %s: %v", name, key)
				}
				seen[key] = true
				for resource, dest := range map[string]*string{"cpu": &entry.CPU, "memory": &entry.Memory} {
					node := sizingChild(request, resource)
					if node == nil {
						continue
					}
					*dest = sizingString(node)
					if (resource == "cpu" && !sizingCPU.MatchString(*dest)) || (resource == "memory" && !sizingMemory.MatchString(*dest)) {
						return nil, fmt.Errorf("unsupported %s quantity at line %d", resource, node.Line)
					}
					if branch == 0 && name == "e2e_minimal" {
						e.targets[[3]string{key[0], key[1], resource}] = node
					}
				}
				if entry.CPU == "" && entry.Memory == "" {
					return nil, fmt.Errorf("missing requests for %v", key)
				}
				if branch == 0 && name == "e2e_minimal" {
					e.entries = append(e.entries, entry)
				}
			}
		}
	}
	if len(e.entries) == 0 {
		return nil, fmt.Errorf("missing limited e2e_minimal requests")
	}
	return e, nil
}

func sizingChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	return findChild(node, key)
}

func sizingString(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return ""
	}
	return node.Value
}

// Verify raw single-line tokens too: YAML folds some multiline scalars to strings
// without newlines. Node columns count runes, whereas replacements use bytes.
func validateSizingNode(node *yaml.Node, lines []string) error {
	if node.Anchor != "" || node.Kind == yaml.AliasNode || node.Tag == "!!merge" || node.Style & ^(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
		return fmt.Errorf("unsupported YAML at line %d", node.Line)
	}
	if node.Kind == yaml.ScalarNode {
		token := node.Value
		switch node.Style {
		case yaml.SingleQuotedStyle:
			token = "'" + token + "'"
		case yaml.DoubleQuotedStyle:
			token = `"` + token + `"`
		}
		if node.Line < 1 || node.Line > len(lines) || node.Column < 1 || node.Column > len([]rune(lines[node.Line-1])) || strings.ContainsAny(token, "\r\n") || !strings.HasPrefix(string([]rune(lines[node.Line-1])[node.Column-1:]), token) {
			return fmt.Errorf("unsupported scalar at line %d", node.Line)
		}
	}
	keys := map[string]bool{}
	for i, child := range node.Content {
		if node.Kind == yaml.MappingNode && i%2 == 0 {
			if child.Kind != yaml.ScalarNode || child.Tag != "!!str" || keys[child.Value] {
				return fmt.Errorf("invalid or duplicate key at line %d", child.Line)
			}
			keys[child.Value] = true
		}
		if err := validateSizingNode(child, lines); err != nil {
			return err
		}
	}
	return nil
}

// Entries returns a copy in source order; absent resources have empty values.
func (e *SizingEditor) Entries() []SizingEntry { return append([]SizingEntry(nil), e.entries...) }

// Apply validates the entire batch before writing, and never inserts targets.
func (e *SizingEditor) Apply(updates []SizingUpdate) error {
	lines := strings.Split(e.original, "\n")
	seen := map[[3]string]bool{}
	for _, update := range updates {
		key := [3]string{update.Deployment, update.Container, update.Resource}
		node := e.targets[key]
		valid := update.Resource == "cpu" && sizingCPU.MatchString(update.NewValue) || update.Resource == "memory" && sizingMemory.MatchString(update.NewValue) && strings.HasSuffix(update.NewValue, "Mi")
		if node == nil || seen[key] || !valid {
			return fmt.Errorf("unknown, duplicate, or invalid sizing update: %+v", update)
		}
		seen[key] = true
		line := lines[node.Line-1]
		start := len(string([]rune(line)[:node.Column-1]))
		if node.Style != 0 {
			start++ // Preserve the original quote characters, not just their style.
		}
		lines[node.Line-1] = line[:start] + update.NewValue + line[start+len(node.Value):]
	}
	info, err := os.Lstat(e.path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("sizing path is not a regular file")
	}
	current, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}
	if string(current) != e.original {
		return fmt.Errorf("sizing file changed since it was read")
	}
	content := strings.Join(lines, "\n")
	if content == e.original {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.path), ".sizing-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err = tmp.WriteString(content); err != nil {
		return err
	}
	if err = tmp.Chmod(info.Mode()); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// Recheck after the potentially slow write/sync, not just before preparing
	// the replacement. This is optimistic protection, not a filesystem lock.
	latestInfo, err := os.Lstat(e.path)
	if err != nil {
		return err
	}
	latest, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}
	if !os.SameFile(info, latestInfo) || string(latest) != e.original {
		return fmt.Errorf("sizing file changed while preparing replacement")
	}
	if err = os.Rename(tmp.Name(), e.path); err != nil {
		return err
	}
	e.original = content
	for _, update := range updates {
		e.targets[[3]string{update.Deployment, update.Container, update.Resource}].Value = update.NewValue
		for i := range e.entries {
			if e.entries[i].Deployment == update.Deployment && e.entries[i].Container == update.Container {
				if update.Resource == "cpu" {
					e.entries[i].CPU = update.NewValue
				} else {
					e.entries[i].Memory = update.NewValue
				}
			}
		}
	}
	return nil
}
