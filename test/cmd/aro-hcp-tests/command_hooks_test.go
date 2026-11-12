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

func TestCommandHooksPreserveExecutionHooks(t *testing.T) {
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
			wrapCommandHooks(parent, commandHooks{BeforePersistent: []commandHook{
				func(*cobra.Command, []string) error {
					record("initialize")
					return nil
				},
			}})
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

func TestCommandHooksOrderAndErrors(t *testing.T) {
	stages := []string{"initialize", "scheduler", "persistent", "pre-run", "concurrency", "run"}
	for _, failAt := range append([]string{""}, stages[:len(stages)-1]...) {
		t.Run("fail-at-"+failAt, func(t *testing.T) {
			var calls []string
			failure := errors.New("hook failed")
			record := func(stage string) commandHook {
				return func(*cobra.Command, []string) error {
					calls = append(calls, stage)
					if stage == failAt {
						return failure
					}
					return nil
				}
			}
			command := &cobra.Command{
				Use:               "extension",
				SilenceUsage:      true,
				SilenceErrors:     true,
				PersistentPreRunE: record("persistent"),
				PersistentPreRun:  func(*cobra.Command, []string) { t.Fatal("PersistentPreRunE must take precedence") },
				PreRunE:           record("pre-run"),
				PreRun:            func(*cobra.Command, []string) { t.Fatal("PreRunE must take precedence") },
				RunE:              record("run"),
			}
			wrapCommandHooks(command, commandHooks{
				BeforePersistent:      []commandHook{record("initialize")},
				BeforePersistentLocal: []commandHook{record("scheduler")},
				AfterPreRun:           []commandHook{record("concurrency")},
			})
			command.SetArgs([]string{})
			err := command.Execute()
			if failAt == "" && err != nil || failAt != "" && !errors.Is(err, failure) {
				t.Fatalf("execution error = %v, fail-at = %q", err, failAt)
			}
			want := stages
			for i, stage := range stages {
				if stage == failAt {
					want = stages[:i+1]
					break
				}
			}
			if strings.Join(calls, ",") != strings.Join(want, ",") {
				t.Fatalf("execution = %v, want %v", calls, want)
			}
		})
	}
}

func TestCommandHooksKeepLocalSetupOutOfDescendants(t *testing.T) {
	for _, scenario := range []string{"command", "inherited", "child-override"} {
		t.Run(scenario, func(t *testing.T) {
			var calls []string
			record := func(stage string) commandHook {
				return func(c *cobra.Command, _ []string) error {
					calls = append(calls, stage+":"+c.Name())
					return nil
				}
			}
			root := &cobra.Command{
				Use:               "root",
				SilenceUsage:      true,
				SilenceErrors:     true,
				PersistentPreRunE: record("ancestor"),
				PersistentPreRun:  func(*cobra.Command, []string) { t.Fatal("ancestor error hook must take precedence") },
			}
			command := &cobra.Command{Use: "extension", RunE: record("run")}
			child := &cobra.Command{Use: "child", RunE: record("run")}
			if scenario == "child-override" {
				child.PersistentPreRunE = record("child-hook")
			}
			command.AddCommand(child)
			wrapCommandHooks(command, commandHooks{
				BeforePersistent:      []commandHook{record("initialize")},
				BeforePersistentLocal: []commandHook{record("scheduler")},
				AfterPreRun:           []commandHook{record("concurrency")},
			})
			// Ancestor inheritance must work even when root registration happens
			// after hook composition, as it does in setupCli.
			root.AddCommand(command)
			args := []string{"extension", "child"}
			want := "initialize:child,ancestor:child,run:child"
			switch scenario {
			case "command":
				args = []string{"extension"}
				want = "initialize:extension,scheduler:extension,ancestor:extension,concurrency:extension,run:extension"
			case "child-override":
				want = "initialize:child,child-hook:child,run:child"
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Join(calls, ",") != want {
				t.Fatalf("execution = %v, want %s", calls, want)
			}
		})
	}
}
