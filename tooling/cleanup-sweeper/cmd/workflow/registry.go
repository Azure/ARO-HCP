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

package workflow

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
)

// Registry holds the available workflows and the options each declares.
type Registry struct {
	workflows []Workflow
	byName    map[string]Workflow
	options   map[string]*pflag.FlagSet
}

// NewRegistry registers workflows in the supplied order, rejecting nil
// workflows and empty, whitespace-padded or duplicate names, and lets each
// workflow declare its options.
func NewRegistry(workflows ...Workflow) (*Registry, error) {
	registry := &Registry{
		workflows: make([]Workflow, 0, len(workflows)),
		byName:    make(map[string]Workflow, len(workflows)),
		options:   make(map[string]*pflag.FlagSet, len(workflows)),
	}
	for _, workflow := range workflows {
		if workflow == nil {
			return nil, errors.New("workflow is nil")
		}
		value := reflect.ValueOf(workflow)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if value.IsNil() {
				return nil, errors.New("workflow is nil")
			}
		}
		name := strings.TrimSpace(workflow.Name())
		if name == "" {
			return nil, errors.New("workflow name is empty")
		}
		if name != workflow.Name() {
			return nil, fmt.Errorf("workflow name %q contains surrounding whitespace", workflow.Name())
		}
		if _, found := registry.byName[name]; found {
			return nil, fmt.Errorf("workflow %q is registered more than once", name)
		}
		registry.workflows = append(registry.workflows, workflow)
		registry.byName[name] = workflow
		flags := pflag.NewFlagSet(name, pflag.ContinueOnError)
		workflow.AttachOptions(flags)
		registry.options[name] = flags
	}
	return registry, nil
}

// Names lists the registered workflow names in registration order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.workflows))
	for _, workflow := range r.workflows {
		names = append(names, workflow.Name())
	}
	return names
}

// Get returns the named workflow.
func (r *Registry) Get(name string) (Workflow, error) {
	workflow, found := r.byName[name]
	if !found {
		return nil, fmt.Errorf("unknown workflow %q (available: %s)", name, strings.Join(r.Names(), ", "))
	}
	return workflow, nil
}

// ApplyOptions sets the selected workflow's options from entries of the form
// "<workflow>.<key>=<value>". Keys are the names the workflow declared, and
// later entries override earlier ones (list-valued options accumulate). An
// unknown workflow, an option meant for a different workflow than the selected
// one, an unknown key, or a value the option rejects is an error.
func (r *Registry) ApplyOptions(selected string, entries []string) error {
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		workflowName, key, dotted := strings.Cut(name, ".")
		workflowName, key = strings.TrimSpace(workflowName), strings.TrimSpace(key)
		if !found || !dotted || workflowName == "" || key == "" {
			return fmt.Errorf("%q must have the form <workflow>.<key>=<value>", entry)
		}
		flags, registered := r.options[workflowName]
		if !registered {
			return fmt.Errorf("%q: unknown workflow %q (available: %s)", entry, workflowName, strings.Join(r.Names(), ", "))
		}
		if workflowName != selected {
			return fmt.Errorf("%q: options for workflow %q cannot be used with --workflow=%s", entry, workflowName, selected)
		}
		if flags.Lookup(key) == nil {
			if !flags.HasFlags() {
				return fmt.Errorf("%q: workflow %q has no options", entry, workflowName)
			}
			return fmt.Errorf("%q: workflow %q has no option %q (valid: %s)", entry, workflowName, key, optionNames(flags))
		}
		if err := flags.Set(key, strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%s.%s: %w", workflowName, key, err)
		}
	}
	return nil
}

// OptionsUsage describes every option declared by the registered workflows,
// grouped by workflow, for command help.
func (r *Registry) OptionsUsage() string {
	var usage strings.Builder
	for _, workflow := range r.workflows {
		flags := r.options[workflow.Name()]
		if !flags.HasFlags() {
			continue
		}
		fmt.Fprintf(&usage, "%s options:\n", workflow.Name())
		flags.VisitAll(func(flag *pflag.Flag) {
			fmt.Fprintf(&usage, "  %s.%s=<%s> (default %s): %s\n", workflow.Name(), flag.Name, flag.Value.Type(), flag.DefValue, flag.Usage)
		})
	}
	return usage.String()
}

func optionNames(flags *pflag.FlagSet) string {
	var names []string
	flags.VisitAll(func(flag *pflag.Flag) { names = append(names, flag.Name) })
	return strings.Join(names, ", ")
}
