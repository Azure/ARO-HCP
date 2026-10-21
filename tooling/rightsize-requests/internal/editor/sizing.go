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
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
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

// Restrict the raw token to unambiguous decimal syntax before parsing Kubernetes
// units. In particular, YAML octal/hex numbers must not become decimal requests.
var sizingQuantityToken = regexp.MustCompile(`^\+?((0|[1-9][0-9]*)(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+|[numkMGTPE]|[KMGTPE]i)?$`)

func sizingQuantity(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!int" && node.Tag != "!!float") || !sizingQuantityToken.MatchString(node.Value) {
		return ""
	}
	quantity, err := resource.ParseQuantity(node.Value)
	if err != nil || quantity.Sign() <= 0 {
		return ""
	}
	return node.Value
}

const additionalSizingInclude = `{{ include "hypershift.additionalMinimalResourceRequests" . | trim | indent 10 | trimSuffix "          " }}`

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
	includeSeen := false
	for i, line := range lines {
		if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
			if strings.TrimSpace(line) == additionalSizingInclude && state == 1 && !includeSeen {
				includeSeen = true
				continue // Keep the source line, but leave it blank in the parsed branch.
			}
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
					*dest = sizingQuantity(node)
					if *dest == "" {
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
	content := strings.Join(lines, "\n")
	if err := e.replace(content); err != nil {
		return err
	}
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

func (e *SizingEditor) replace(content string) error {
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
	return nil
}

const additionalSizingPath = "hypershift.additionalMinimalResourceRequests"

var additionalSizingID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
var additionalSizingName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type AdditionalSizingEntry struct {
	ID string
	SizingEntry
}

// AdditionalSizingEditor exposes configured, effective dev entries. Regular Apply
// cannot insert identities or resources; experimental CPU staging is explicit.
type AdditionalSizingEditor struct {
	file              SizingEditor
	entries           []AdditionalSizingEntry
	empty             map[string]*yaml.Node
	devFields         map[string]bool
	identityConflicts []string
}

func NewAdditionalSizing(path string) (*AdditionalSizingEditor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root, extra yaml.Node
	d := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := d.Decode(&root); err != nil {
		return nil, err
	}
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one config document: %v", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, fmt.Errorf("expected config mapping document")
	}
	e := &AdditionalSizingEditor{file: SizingEditor{path: path, original: string(data)}, empty: map[string]*yaml.Node{}, devFields: map[string]bool{}}
	lines := strings.Split(string(data), "\n")
	// Only paths traversed by the line editor need this restricted YAML shape;
	// unrelated config values may contain templates, lists or block scalars.
	checkMap := func(node *yaml.Node) error {
		if node.Kind != yaml.MappingNode || node.Tag != "!!map" || node.Anchor != "" || (node.Style != 0 && (node.Style != yaml.FlowStyle || len(node.Content) != 0)) {
			return fmt.Errorf("expected plain config mapping at line %d", node.Line)
		}
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Style != 0 || key.Anchor != "" || seen[key.Value] {
				return fmt.Errorf("invalid or duplicate config key at line %d", key.Line)
			}
			if err := validateSizingNode(key, lines); err != nil {
				return err
			}
			seen[key.Value] = true
		}
		return nil
	}
	effective := map[string]map[string]string{}
	for _, prefix := range []string{"defaults", "clouds.dev.defaults"} {
		node := root.Content[0]
		segments := strings.Split(prefix+"."+additionalSizingPath, ".")
		for i, segment := range segments {
			if err := checkMap(node); err != nil {
				return nil, err
			}
			if prefix == "clouds.dev.defaults" && len(node.Content) == 0 {
				e.empty[strings.Join(segments[:i], ".")] = node
			}
			node = findChild(node, segment)
			if node == nil {
				break
			}
		}
		if node == nil {
			continue
		}
		if err := checkMap(node); err != nil {
			return nil, err
		}
		if prefix == "clouds.dev.defaults" && len(node.Content) == 0 {
			e.empty[prefix+"."+additionalSizingPath] = node
		}
		for i := 0; i < len(node.Content); i += 2 {
			id, object := node.Content[i].Value, node.Content[i+1]
			if !additionalSizingID.MatchString(id) {
				return nil, fmt.Errorf("unsafe additional sizing ID %q", id)
			}
			if err := checkMap(object); err != nil {
				return nil, err
			}
			if prefix == "clouds.dev.defaults" && len(object.Content) == 0 {
				e.empty[prefix+"."+additionalSizingPath+"."+id] = object
			}
			if effective[id] == nil {
				effective[id] = map[string]string{}
			}
			for j := 0; j < len(object.Content); j += 2 {
				key, value := object.Content[j].Value, object.Content[j+1]
				text := sizingString(value)
				valid := false
				switch key {
				case "deploymentName", "containerName":
					valid = additionalSizingName.MatchString(text)
				case "cpu", "memory":
					text = sizingQuantity(value)
					valid = text != ""
				}
				if !valid {
					return nil, fmt.Errorf("invalid additional sizing %s.%s at line %d", id, key, value.Line)
				}
				if err := validateSizingNode(value, lines); err != nil {
					return nil, err
				}
				if prefix == "clouds.dev.defaults" && (key == "deploymentName" || key == "containerName") && effective[id][key] != "" && effective[id][key] != text {
					e.identityConflicts = append(e.identityConflicts, id+"."+key)
				}
				effective[id][key] = text
				if prefix == "clouds.dev.defaults" {
					e.devFields[prefix+"."+additionalSizingPath+"."+id+"."+key] = true
				}
			}
		}
	}
	seen := map[[2]string]bool{}
	for id, fields := range effective {
		entry := AdditionalSizingEntry{id, SizingEntry{fields["deploymentName"], fields["containerName"], fields["cpu"], fields["memory"]}}
		key := [2]string{entry.Deployment, entry.Container}
		if key[0] == "" || key[1] == "" || seen[key] {
			return nil, fmt.Errorf("missing or duplicate additional sizing identity %q", id)
		}
		if err := targets.ValidateResourceRequestOverride(entry.Deployment, entry.Container); err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		seen[key] = true
		e.entries = append(e.entries, entry)
	}
	sort.Slice(e.entries, func(i, j int) bool { return e.entries[i].ID < e.entries[j].ID })
	return e, nil
}

func (e *AdditionalSizingEditor) Entries() []AdditionalSizingEntry {
	return append([]AdditionalSizingEntry(nil), e.entries...)
}

// Apply copies identities into dev when needed, but changes only existing
// effective resource knobs. Stage the line-editor upserts before replacing config.
func (e *AdditionalSizingEditor) Apply(updates []SizingUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	validated, err := e.stage(updates)
	if err != nil {
		return err
	}
	return (&ExperimentalCPUEdit{editor: e, staged: validated, original: e.file.original}).Apply()
}

func (e *AdditionalSizingEditor) stage(updates []SizingUpdate) (*AdditionalSizingEditor, error) {
	var edits []Update
	seen := map[[3]string]bool{}
	identities := map[string]bool{}
	for _, update := range updates {
		var entry *AdditionalSizingEntry
		for i := range e.entries {
			if e.entries[i].Deployment == update.Deployment && e.entries[i].Container == update.Container {
				entry = &e.entries[i]
			}
		}
		key := [3]string{update.Deployment, update.Container, update.Resource}
		valid := entry != nil && (update.Resource == "cpu" && entry.CPU != "" && sizingCPU.MatchString(update.NewValue) || update.Resource == "memory" && entry.Memory != "" && sizingMemory.MatchString(update.NewValue) && strings.HasSuffix(update.NewValue, "Mi"))
		if !valid || seen[key] {
			return nil, fmt.Errorf("unknown, duplicate, or invalid additional sizing update: %+v", update)
		}
		seen[key] = true
		path := "clouds.dev.defaults." + additionalSizingPath + "." + entry.ID + "."
		for _, identity := range []struct{ key, value string }{{"deploymentName", entry.Deployment}, {"containerName", entry.Container}} {
			if !e.devFields[path+identity.key] && !identities[path+identity.key] {
				edits = append(edits, Update{Path: path + identity.key, NewValue: strconv.Quote(identity.value)})
				identities[path+identity.key] = true
			}
		}
		edits = append(edits, Update{Path: path + update.Resource, NewValue: update.NewValue})
	}
	if len(edits) == 0 {
		return e, nil
	}
	lines := strings.Split(e.file.original, "\n")
	for path, node := range e.empty {
		needed := false
		for _, edit := range edits {
			if path == "" || strings.HasPrefix(edit.Path, path+".") {
				needed = true
			}
		}
		if !needed {
			continue
		}
		line := lines[node.Line-1]
		column := len(string([]rune(line)[:node.Column-1]))
		if !strings.HasPrefix(line[column:], "{}") {
			return nil, fmt.Errorf("unsupported empty mapping at line %d", node.Line)
		}
		lines[node.Line-1] = line[:column] + line[column+2:]
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.file.path), ".additional-sizing-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.WriteString(strings.Join(lines, "\n")); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	staged, err := New(tmp.Name())
	if err != nil {
		return nil, err
	}
	if err := staged.Upsert(edits); err != nil {
		return nil, err
	}
	validated, err := NewAdditionalSizing(tmp.Name())
	if err != nil {
		return nil, err
	}
	if len(validated.entries) != len(e.entries) {
		return nil, fmt.Errorf("staged additional sizing edit changed configured targets")
	}
	for i, original := range e.entries {
		want := original
		for _, update := range updates {
			if update.Deployment == want.Deployment && update.Container == want.Container {
				if update.Resource == "cpu" {
					want.CPU = update.NewValue
				} else {
					want.Memory = update.NewValue
				}
			}
		}
		if validated.entries[i] != want {
			return nil, fmt.Errorf("staged additional sizing edit did not preserve target %s", want.ID)
		}
	}
	return validated, nil
}

// ExperimentalCPUEdit is a validated single-file edit. Staging never changes the
// destination; Apply retains optimistic stale checks and atomic mode preservation.
type ExperimentalCPUEdit struct {
	editor, staged *AdditionalSizingEditor
	original       string
}

func (edit *ExperimentalCPUEdit) Apply() error {
	e, validated := edit.editor, edit.staged
	if e.file.original != edit.original {
		return fmt.Errorf("sizing editor changed since staging")
	}
	if err := e.file.replace(validated.file.original); err != nil {
		return err
	}
	e.entries, e.empty, e.devFields = validated.entries, validated.empty, validated.devFields
	return nil
}

// StageExperimentalCPU intentionally permits CPU-only seeds. The caller must
// establish catalog membership and observed baselines; IDs cannot change identity.
// Existing resources (including memory) and all global defaults are preserved.
func (e *AdditionalSizingEditor) StageExperimentalCPU(updates []AdditionalSizingEntry) (*ExperimentalCPUEdit, error) {
	if len(e.identityConflicts) > 0 {
		return nil, fmt.Errorf("experimental CPU default/dev identity conflict: %s", strings.Join(e.identityConflicts, ", "))
	}
	clone := *e
	clone.entries = e.Entries()
	var changes []SizingUpdate
	for _, update := range updates {
		if !additionalSizingID.MatchString(update.ID) || !additionalSizingName.MatchString(update.Deployment) || !additionalSizingName.MatchString(update.Container) || !sizingCPU.MatchString(update.CPU) || update.Memory != "" {
			return nil, fmt.Errorf("invalid experimental CPU update: %+v", update)
		}
		if err := targets.ValidateResourceRequestOverride(update.Deployment, update.Container); err != nil {
			return nil, fmt.Errorf("%s: %w", update.ID, err)
		}
		found := false
		for i, entry := range clone.entries {
			sameIdentity := entry.Deployment == update.Deployment && entry.Container == update.Container
			if (entry.ID == update.ID) != sameIdentity {
				return nil, fmt.Errorf("experimental CPU ID collision: %s (%s/%s)", update.ID, update.Deployment, update.Container)
			}
			if sameIdentity {
				found = true
				if entry.CPU == "" {
					clone.entries[i].CPU = update.CPU
				}
			}
		}
		if !found {
			clone.entries = append(clone.entries, update)
		}
		changes = append(changes, SizingUpdate{update.Deployment, update.Container, "cpu", update.CPU})
	}
	sort.Slice(clone.entries, func(i, j int) bool { return clone.entries[i].ID < clone.entries[j].ID })
	validated, err := clone.stage(changes)
	if err != nil {
		return nil, err
	}
	return &ExperimentalCPUEdit{editor: e, staged: validated, original: e.file.original}, nil
}
