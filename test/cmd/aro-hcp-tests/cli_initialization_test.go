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

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExtensionInitializationPreservesExecutionHooks(t *testing.T) {
	for _, hook := range []string{"inherited", "persistent", "persistent-error", "local-error"} {
		t.Run(hook, func(t *testing.T) {
			var calls []string
			record := func(name string) { calls = append(calls, name) }
			hookError := errors.New("hook failed")
			parent := &cobra.Command{
				Use: "extension",
				PersistentPreRun: func(*cobra.Command, []string) {
					record("parent")
				},
			}
			child := &cobra.Command{
				Use: "child",
				PreRun: func(*cobra.Command, []string) {
					record("local")
				},
				Run: func(*cobra.Command, []string) {
					record("run")
				},
			}
			want := "initialize,parent,local,run"
			wantError := false
			switch hook {
			case "persistent":
				child.PersistentPreRun = func(*cobra.Command, []string) { record("child") }
				want = "initialize,child,local,run"
			case "persistent-error":
				child.PersistentPreRun = func(*cobra.Command, []string) { t.Fatal("non-error hook must not run") }
				child.PersistentPreRunE = func(*cobra.Command, []string) error {
					record("child")
					return hookError
				}
				want, wantError = "initialize,child", true
			case "local-error":
				child.PreRunE = func(*cobra.Command, []string) error {
					record("local-error")
					return hookError
				}
				want, wantError = "initialize,parent,local-error", true
			}
			parent.AddCommand(child)
			wrapExtensionInitialization(parent, func() { record("initialize") })
			root := &cobra.Command{Use: "root", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(parent)
			root.SetArgs([]string{"extension", "child"})
			err := root.Execute()
			if wantError && !errors.Is(err, hookError) || !wantError && err != nil {
				t.Fatalf("unexpected execution error: %v", err)
			}
			if got := strings.Join(calls, ","); got != want {
				t.Fatalf("execution order = %q, want %q", got, want)
			}
		})
	}
}
