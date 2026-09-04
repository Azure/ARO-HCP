// Copyright 2025 Microsoft Corporation
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

package settings

import (
	"testing"
)

func TestExpandBashSubstring(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{
			name:  "plain variable",
			input: "${USER}",
			env:   map[string]string{"USER": "testuser"},
			want:  "testuser",
		},
		{
			name:  "plain variable short form",
			input: "$USER",
			env:   map[string]string{"USER": "testuser"},
			want:  "testuser",
		},
		{
			name:  "substring offset and length",
			input: "${USER:0:4}",
			env:   map[string]string{"USER": "testuser"},
			want:  "test",
		},
		{
			name:  "negative offset",
			input: "${BUILD_ID: -7}",
			env:   map[string]string{"BUILD_ID": "1234567890"},
			want:  "4567890",
		},
		{
			name:  "literal prefix with substring",
			input: "p${USER:0:4}",
			env:   map[string]string{"USER": "testuser"},
			want:  "ptest",
		},
		{
			name:  "literal prefix with negative offset",
			input: "j${BUILD_ID: -7}",
			env:   map[string]string{"BUILD_ID": "1234567890"},
			want:  "j4567890",
		},
		{
			name:  "offset only no length",
			input: "${VAR:3}",
			env:   map[string]string{"VAR": "abcdefgh"},
			want:  "defgh",
		},
		{
			name:  "unset variable returns empty",
			input: "${NONEXISTENT:0:4}",
			env:   map[string]string{},
			want:  "",
		},
		{
			name:  "offset beyond string length",
			input: "${VAR:100:4}",
			env:   map[string]string{"VAR": "short"},
			want:  "",
		},
		{
			name:  "length exceeds remaining",
			input: "${VAR:3:100}",
			env:   map[string]string{"VAR": "abcde"},
			want:  "de",
		},
		{
			name:  "negative offset exceeds string length clamps to zero",
			input: "${VAR: -100}",
			env:   map[string]string{"VAR": "abc"},
			want:  "abc",
		},
		{
			name:  "no expansion needed",
			input: "literal-string",
			env:   map[string]string{},
			want:  "literal-string",
		},
		{
			name:  "empty input",
			input: "",
			env:   map[string]string{},
			want:  "",
		},
		{
			name:  "shell metacharacters are not executed",
			input: "$(whoami)",
			env:   map[string]string{},
			want:  "$(whoami)",
		},
		{
			name:  "backtick injection is literal",
			input: "prefix-`whoami`-suffix",
			env:   map[string]string{},
			want:  "prefix-`whoami`-suffix",
		},
		{
			name:  "semicolon injection is literal",
			input: "value; rm -rf /",
			env:   map[string]string{},
			want:  "value; rm -rf /",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := expandBashSubstring(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("expandBashSubstring(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("expandBashSubstring(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("USER", "hlipsig1")
	t.Setenv("BUILD_ID", "build-1234567")

	env := Environment{
		Name: "pers",
		Defaults: Parameters{
			Cloud:             "dev",
			Ev2Cloud:          "public",
			Region:            "westus3",
			CxStamp:           "1",
			RegionShortSuffix: "${USER:0:4}",
		},
	}

	got, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.RegionShortSuffix != "hlip" {
		t.Errorf("RegionShortSuffix = %q, want %q", got.RegionShortSuffix, "hlip")
	}
	if got.Cloud != "dev" {
		t.Errorf("Cloud = %q, want %q", got.Cloud, "dev")
	}

	env2 := Environment{
		Name: "prow",
		Defaults: Parameters{
			Cloud:               "dev",
			Ev2Cloud:            "public",
			Region:              "westus3",
			CxStamp:             "1",
			RegionShortOverride: "j${BUILD_ID: -7}",
		},
	}

	got2, err := Resolve(env2)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got2.RegionShortOverride != "j1234567" {
		t.Errorf("RegionShortOverride = %q, want %q", got2.RegionShortOverride, "j1234567")
	}
}
