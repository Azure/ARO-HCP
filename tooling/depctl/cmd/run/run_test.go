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

package run

import (
	"bytes"
	"testing"
)

func TestSimulateRejectsPrepareFlags(t *testing.T) {
	command, err := NewCommand("main")
	if err != nil {
		t.Fatalf("NewCommand() error = %v", err)
	}
	for _, arguments := range [][]string{
		{"--mode", "simulate", "--repo", "."},
		{"--mode", "simulate", "--target", "example.com/tool=1.1.0"},
	} {
		command.SetArgs(arguments)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		if err := command.Execute(); err == nil {
			t.Fatalf("simulate must reject %v", arguments)
		}
	}
}
