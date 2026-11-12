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
	"context"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type fakeWorkflow struct {
	name   string
	limit  int
	region string
	groups []string
	quiet  bool
}

func (f *fakeWorkflow) Name() string { return f.name }

func (f *fakeWorkflow) AttachOptions(flags *pflag.FlagSet) {
	if f.quiet {
		return
	}
	flags.IntVar(&f.limit, "limit", 5, "Maximum items.")
	flags.StringVar(&f.region, "region", "", "Region name.")
	flags.StringSliceVar(&f.groups, "group", nil, "Group (repeatable).")
}

func (f *fakeWorkflow) Validate(Common) error { return nil }
func (f *fakeWorkflow) LogValues() []any      { return nil }
func (f *fakeWorkflow) Run(context.Context, Common, azcore.TokenCredential) error {
	return nil
}

func TestNewRegistryRejectsInvalidWorkflows(t *testing.T) {
	var nilWorkflow *fakeWorkflow
	for name, test := range map[string]struct {
		workflows []Workflow
		wantErr   string
	}{
		"nil":            {workflows: []Workflow{nil}, wantErr: "workflow is nil"},
		"typed nil":      {workflows: []Workflow{nilWorkflow}, wantErr: "workflow is nil"},
		"empty name":     {workflows: []Workflow{&fakeWorkflow{name: " "}}, wantErr: "name is empty"},
		"padded name":    {workflows: []Workflow{&fakeWorkflow{name: " a"}}, wantErr: "surrounding whitespace"},
		"duplicate name": {workflows: []Workflow{&fakeWorkflow{name: "a"}, &fakeWorkflow{name: "a"}}, wantErr: "more than once"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRegistry(test.workflows...); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestRegistryGet(t *testing.T) {
	registry, err := NewRegistry(&fakeWorkflow{name: "a"}, &fakeWorkflow{name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if workflow, err := registry.Get("b"); err != nil || workflow.Name() != "b" {
		t.Fatalf("Get(b) = %v, %v", workflow, err)
	}
	if _, err := registry.Get("c"); err == nil || !strings.Contains(err.Error(), "available: a, b") {
		t.Fatalf("error = %v, want available workflows listed", err)
	}
}

func TestApplyOptions(t *testing.T) {
	newRegistry := func(t *testing.T) (*Registry, *fakeWorkflow, *fakeWorkflow) {
		t.Helper()
		a, b := &fakeWorkflow{name: "a"}, &fakeWorkflow{name: "b"}
		registry, err := NewRegistry(a, b, &fakeWorkflow{name: "none", quiet: true})
		if err != nil {
			t.Fatal(err)
		}
		return registry, a, b
	}

	t.Run("sets values, later entries override, lists accumulate", func(t *testing.T) {
		registry, a, _ := newRegistry(t)
		err := registry.ApplyOptions("a", []string{"a.limit=1", "a.region= west ", "a.limit=7", "a.group=x", "a.group=y,z"})
		if err != nil {
			t.Fatal(err)
		}
		if a.limit != 7 || a.region != "west" || strings.Join(a.groups, ",") != "x,y,z" {
			t.Fatalf("limit=%d region=%q groups=%v", a.limit, a.region, a.groups)
		}
	})

	t.Run("values may contain equals signs", func(t *testing.T) {
		registry, a, _ := newRegistry(t)
		if err := registry.ApplyOptions("a", []string{"a.region=k=v"}); err != nil {
			t.Fatal(err)
		}
		if a.region != "k=v" {
			t.Fatalf("region = %q", a.region)
		}
	})

	t.Run("defaults apply without entries", func(t *testing.T) {
		registry, a, _ := newRegistry(t)
		if err := registry.ApplyOptions("a", nil); err != nil {
			t.Fatal(err)
		}
		if a.limit != 5 {
			t.Fatalf("limit = %d, want default 5", a.limit)
		}
	})

	for name, test := range map[string]struct {
		selected string
		entry    string
		wantErr  string
	}{
		"missing equals":           {"a", "a.limit", "must have the form <workflow>.<key>=<value>"},
		"missing dot":              {"a", "limit=1", "must have the form <workflow>.<key>=<value>"},
		"empty key":                {"a", "a.=1", "must have the form"},
		"unknown workflow":         {"a", "c.limit=1", `unknown workflow "c" (available: a, b, none)`},
		"other workflow's option":  {"a", "b.limit=1", `options for workflow "b" cannot be used with --workflow=a`},
		"unknown key lists valid":  {"a", "a.nope=1", `no option "nope" (valid: group, limit, region)`},
		"workflow without options": {"none", "none.limit=1", `workflow "none" has no options`},
		"invalid value":            {"a", "a.limit=many", "a.limit:"},
	} {
		t.Run(name, func(t *testing.T) {
			registry, _, _ := newRegistry(t)
			if err := registry.ApplyOptions(test.selected, []string{test.entry}); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestOptionsUsage(t *testing.T) {
	registry, err := NewRegistry(&fakeWorkflow{name: "a"}, &fakeWorkflow{name: "none", quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	usage := registry.OptionsUsage()
	for _, want := range []string{"a options:", "a.limit=<int> (default 5): Maximum items.", "a.region=<string> (default ): Region name."} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage missing %q:\n%s", want, usage)
		}
	}
	if strings.Contains(usage, "none") {
		t.Errorf("workflows without options must not appear:\n%s", usage)
	}
}
