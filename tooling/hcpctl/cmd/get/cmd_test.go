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

package getcmd

import (
	"context"
	"testing"
)

func TestParseResourceArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantResource string
		wantName     string
		wantError    bool
	}{
		{name: "resource", args: []string{"pods"}, wantResource: "pods"},
		{name: "separate name", args: []string{"pod", "test"}, wantResource: "pod", wantName: "test"},
		{name: "slash name", args: []string{"pod/test"}, wantResource: "pod", wantName: "test"},
		{name: "invalid slash", args: []string{"pod/test/extra"}, wantError: true},
		{name: "mixed forms", args: []string{"pod/test", "extra"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource, name, err := parseResourceArgs(test.args)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %v", err, test.wantError)
			}
			if resource != test.wantResource || name != test.wantName {
				t.Errorf("got %q %q, want %q %q", resource, name, test.wantResource, test.wantName)
			}
		})
	}
}

func TestResolveServerURLAndHostname(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "https://example.eastus2.kusto.windows.net", want: "https://example.eastus2.kusto.windows.net"},
		{input: "example.eastus2.kusto.windows.net", want: "https://example.eastus2.kusto.windows.net"},
	}
	for _, test := range tests {
		endpoint, err := resolveServer(context.Background(), test.input)
		if err != nil {
			t.Fatalf("resolveServer(%q) returned an error: %v", test.input, err)
		}
		if endpoint.String() != test.want {
			t.Errorf("resolveServer(%q) = %q, want %q", test.input, endpoint.String(), test.want)
		}
	}
}

func TestCommandHasKubectlLikeFlags(t *testing.T) {
	command, err := NewCommand("main")
	if err != nil {
		t.Fatalf("NewCommand returned an error: %v", err)
	}
	for _, name := range []string{"server", "cluster", "namespace", "output", "all-namespaces", "show-kind", "no-kusto-timestamp", "no-ts"} {
		if command.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s was not registered", name)
		}
	}
	if command.Flags().Lookup("show-source") != nil {
		t.Error("obsolete flag --show-source was registered")
	}
	short := map[string]string{"server": "s", "cluster": "c", "namespace": "n", "output": "o", "all-namespaces": "A"}
	for name, shorthand := range short {
		if got := command.Flags().Lookup(name).Shorthand; got != shorthand {
			t.Errorf("--%s shorthand = %q, want %q", name, got, shorthand)
		}
	}
}
