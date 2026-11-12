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

// Package consumers maps fixed consumer IDs to reviewed repository commands.
//
// A plan may only name an ID from this registry. Commands, arguments and
// environment come from compiled code, never from a record, log or proposal.
package consumers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Prerequisite is something a consumer needs before it can run.
type Prerequisite struct {
	// Binary must be resolvable on PATH.
	Binary string
	// Path must exist relative to the repository root.
	Path string
}

// String renders the prerequisite for flag help output.
func (p Prerequisite) String() string {
	if p.Binary != "" {
		return "binary:" + p.Binary
	}
	return "path:" + p.Path
}

// Spec is one registered consumer.
type Spec struct {
	ID            string
	Description   string
	Program       string
	Arguments     []string
	Directory     string
	Environment   []string
	Prerequisites []Prerequisite
	Timeout       time.Duration
	// AllowedParams lists the typed parameters the consumer accepts.
	AllowedParams []string
	// Preflight verifies the executable identity a consumer is about to use.
	// A mismatch is incomplete evidence, never a pass.
	Preflight func(root string, params map[string]string) error
	// SelectedBinary names the tool that a phase-specific selection replaces.
	SelectedBinary string
	// AliasOnPath resolves the selected binary by name through a temporary
	// directory, for consumers that invoke the tool by name.
	AliasOnPath bool
	// SelectionEnvironment renders an environment that selects the binary, for
	// consumers that accept the selection through a variable.
	SelectionEnvironment func(executablePath string) []string
}

var (
	paramTool    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	paramModule  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*$`)
	paramVersion = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+-]*$`)
	paramPath    = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
)

// ErrPrerequisite reports an unmet or mismatched prerequisite.
var ErrPrerequisite = errors.New("prerequisite is not satisfied")

// ValidateParams checks typed parameters against the consumer's allow list.
func (s Spec) ValidateParams(params map[string]string) error {
	allowed := map[string]bool{}
	for _, name := range s.AllowedParams {
		allowed[name] = true
	}
	for name := range params {
		if !allowed[name] {
			return fmt.Errorf("consumer %s does not accept parameter %q", s.ID, name)
		}
	}
	for name, value := range params {
		var pattern *regexp.Regexp
		switch name {
		case "tool":
			pattern = paramTool
		case "module":
			pattern = paramModule
		case "version", "expectedVersion":
			pattern = paramVersion
		case "executablePath":
			pattern = paramPath
		default:
			return fmt.Errorf("consumer %s has no validator for parameter %q", s.ID, name)
		}
		if !pattern.MatchString(value) {
			return fmt.Errorf("consumer %s parameter %s has an invalid value", s.ID, name)
		}
	}
	return nil
}

// Expand resolves the argv for one invocation. Parameters are substituted into
// reviewed placeholders only; unknown placeholders are refused.
// Resolves the argv for one invocation. Parameters are substituted into
// reviewed placeholders only; unknown placeholders are refused.
func (s Spec) Expand(root string, params map[string]string) ([]string, error) {
	if err := s.ValidateParams(params); err != nil {
		return nil, err
	}
	expand := func(value string) (string, error) {
		return template(value, params)
	}
	arguments := make([]string, 0, len(s.Arguments))
	for _, argument := range s.Arguments {
		resolved, err := expand(argument)
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, resolved)
	}
	return arguments, nil
}

// Missing returns unmet prerequisites for a repository root.
//
// A selected executable is checked directly and replaces the PATH lookup for
// the binary it stands in for.
func (s Spec) Missing(root string, params map[string]string) []string {
	var missing []string
	selected := params["executablePath"]
	if selected != "" {
		info, err := os.Stat(selected)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			missing = append(missing, "executable:"+selected)
		}
	}
	for _, requirement := range s.Prerequisites {
		if requirement.Binary != "" {
			if selected != "" && requirement.Binary == s.SelectedBinary {
				continue
			}
			if _, err := exec.LookPath(requirement.Binary); err != nil {
				missing = append(missing, requirement.String())
			}
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(requirement.Path)))
		if err != nil || len(matches) == 0 {
			missing = append(missing, requirement.String())
		}
	}
	return missing
}

// Registry is an immutable set of consumers.
type Registry struct {
	specs map[string]Spec
}

// NewRegistry builds a registry, refusing duplicate IDs.
func NewRegistry(specs ...Spec) (*Registry, error) {
	registry := &Registry{specs: make(map[string]Spec, len(specs))}
	for _, spec := range specs {
		if spec.ID == "" {
			return nil, fmt.Errorf("consumer spec requires an ID")
		}
		if _, exists := registry.specs[spec.ID]; exists {
			return nil, fmt.Errorf("duplicate consumer ID %q", spec.ID)
		}
		if spec.Program == "" {
			return nil, fmt.Errorf("consumer %s requires a program", spec.ID)
		}
		if spec.Timeout <= 0 {
			return nil, fmt.Errorf("consumer %s requires a positive timeout", spec.ID)
		}
		registry.specs[spec.ID] = spec
	}
	return registry, nil
}

// Get returns a registered consumer.
func (r *Registry) Get(id string) (Spec, error) {
	spec, ok := r.specs[id]
	if !ok {
		return Spec{}, fmt.Errorf("consumer %q is not registered", id)
	}
	return spec, nil
}

// IDs lists registered consumers in sorted order.
func (r *Registry) IDs() []string {
	ids := make([]string, 0, len(r.specs))
	for id := range r.specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Default returns the consumers registered for the ARO-HCP repository.
//
// Every entry runs repository code that already exists: no validation step is
// replaced by a substitute check and no fixture is rewritten to pass. Entries
// that need a specific executable verify its identity before running, because a
// newer tool must never be checked by an older binary.
func Default() *Registry {
	specs := []Spec{
		{
			ID:          "ci-versions-verify",
			Description: "verify openshift-ci tool versions agree with the Dockerfile args",
			Program:     "make",
			Arguments:   []string{"-C", "dev-infrastructure/openshift-ci", "verify"},
			Timeout:     5 * time.Minute,
		},
		{
			ID:             "bicep-fixtures",
			Description:    "compile Bicep sources with the pinned compiler and diff the golden fixtures",
			Program:        "make",
			Arguments:      []string{"verify-bicep-fixtures"},
			Prerequisites:  []Prerequisite{{Binary: "bicep"}, {Binary: "jq"}},
			Timeout:        20 * time.Minute,
			AllowedParams:  []string{"expectedVersion", "executablePath"},
			Preflight:      bicepIdentity,
			SelectedBinary: "bicep",
			SelectionEnvironment: func(executablePath string) []string {
				return []string{"BICEP=" + executablePath}
			},
		},
		{
			ID:             "promtool-rules",
			Description:    "run the prometheus-rules Go consumers against the pinned promtool",
			Program:        "go",
			Arguments:      []string{"-C", "tooling/prometheus-rules", "test", "-count=1", "./..."},
			Prerequisites:  []Prerequisite{{Binary: "go"}, {Binary: "promtool"}},
			Timeout:        20 * time.Minute,
			Environment:    []string{"GOTOOLCHAIN=local"},
			AllowedParams:  []string{"expectedVersion", "executablePath"},
			Preflight:      promtoolIdentity,
			SelectedBinary: "promtool",
			AliasOnPath:    true,
		},
		{
			ID:            "yamlfmt-verify",
			Description:   "verify YAML formatting with the pinned formatter",
			Program:       "make",
			Arguments:     []string{"verify-yamlfmt"},
			Prerequisites: []Prerequisite{{Binary: "make"}, {Binary: "go"}},
			Timeout:       20 * time.Minute,
		},
		{
			ID:            "bingo-get",
			Description:   "install one pinned tool and regenerate the bingo variable helpers",
			Program:       "bingo",
			Arguments:     []string{"get", "{{tool}}@{{version}}"},
			Prerequisites: []Prerequisite{{Binary: "bingo"}},
			Timeout:       30 * time.Minute,
			AllowedParams: []string{"tool", "version"},
		},
		{
			ID:            "bingo-regenerate",
			Description:   "re-run the native installer to confirm the generated helpers are stable",
			Program:       "bingo",
			Arguments:     []string{"get"},
			Prerequisites: []Prerequisite{{Binary: "bingo"}},
			Timeout:       30 * time.Minute,
		},
	}
	registry, err := NewRegistry(specs...)
	if err != nil {
		panic(err)
	}
	return registry
}

// Requires the compiler on PATH to be the expected version.
func bicepIdentity(_ string, params map[string]string) error {
	return expectedToolVersion("bicep", "--version", params)
}

// Requires the promtool on PATH to be the expected version.
func promtoolIdentity(_ string, params map[string]string) error {
	return expectedToolVersion("promtool", "--version", params)
}

// Compares a reported tool version with the planned one.
func expectedToolVersion(tool, argument string, params map[string]string) error {
	expected := strings.TrimPrefix(params["expectedVersion"], "v")
	if expected == "" {
		return fmt.Errorf("%w: no expected %s version was supplied", ErrPrerequisite, tool)
	}
	program := tool
	if executable := params["executablePath"]; executable != "" {
		program = executable
	}
	output, err := commandOutput(program, argument)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPrerequisite, err)
	}
	found := reportedVersion.FindString(output)
	if found == "" {
		return fmt.Errorf("%w: %s did not report a version", ErrPrerequisite, tool)
	}
	if found != expected {
		return fmt.Errorf("%w: %s reports version %s, expected %s", ErrPrerequisite, tool, found, expected)
	}
	return nil
}

var reportedVersion = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*`)

// Runs a bounded version probe.
func commandOutput(program string, arguments ...string) (string, error) {
	runContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(runContext, program, arguments...)
	configureProcessGroup(command)
	command.Cancel = func() error { return terminateProcessTree(command.Process) }
	command.WaitDelay = 5 * time.Second
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", program, strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String() + stderr.String(), nil
}

// Substitutes reviewed placeholders in a consumer argument.
func template(value string, params map[string]string) (string, error) {
	output := make([]byte, 0, len(value))
	for index := 0; index < len(value); {
		start := indexOf(value[index:], "{{")
		if start < 0 {
			output = append(output, value[index:]...)
			break
		}
		output = append(output, value[index:index+start]...)
		end := indexOf(value[index+start:], "}}")
		if end < 0 {
			return "", fmt.Errorf("unterminated placeholder in %q", value)
		}
		name := value[index+start+2 : index+start+end]
		resolved, ok := params[name]
		if !ok {
			return "", fmt.Errorf("placeholder {{%s}} has no parameter", name)
		}
		output = append(output, resolved...)
		index = index + start + end + 2
	}
	return string(output), nil
}

// Returns the first index of a substring, or -1.
func indexOf(value, needle string) int {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
