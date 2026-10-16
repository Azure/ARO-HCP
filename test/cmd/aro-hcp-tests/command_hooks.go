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

import "github.com/spf13/cobra"

type commandHook func(*cobra.Command, []string) error

type commandHooks struct {
	// BeforePersistent applies throughout the command's subtree, including
	// descendants that override the inherited persistent pre-run hook.
	BeforePersistent []commandHook
	// BeforePersistentLocal runs only when this command itself is executed.
	BeforePersistentLocal []commandHook
	// AfterPreRun runs only on this command, after its existing local pre-run.
	AfterPreRun []commandHook
}

func runCommandHooks(hooks []commandHook, command *cobra.Command, args []string) error {
	for _, hook := range hooks {
		if err := hook(command, args); err != nil {
			return err
		}
	}
	return nil
}

// wrapCommandHooks preserves Cobra's error-hook precedence and nearest-ancestor
// inheritance. Only subtree prerequisites cross child hook overrides; local
// setup does not leak into descendants. Help does not execute these hooks.
func wrapCommandHooks(command *cobra.Command, hooks commandHooks) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c == command || c.PersistentPreRunE != nil || c.PersistentPreRun != nil {
			preRunE, preRun := c.PersistentPreRunE, c.PersistentPreRun
			c.PersistentPreRunE = func(selected *cobra.Command, args []string) error {
				if err := runCommandHooks(hooks.BeforePersistent, selected, args); err != nil {
					return err
				}
				if selected == command {
					if err := runCommandHooks(hooks.BeforePersistentLocal, selected, args); err != nil {
						return err
					}
				}
				if preRunE != nil {
					return preRunE(selected, args)
				}
				if preRun != nil {
					preRun(selected, args)
					return nil
				}
				// Installing a prerequisite on a command without its own hook
				// must not suppress the hook it previously inherited from outside
				// this subtree. Resolve it at execution, after root registration.
				for parent := command.Parent(); parent != nil; parent = parent.Parent() {
					if parent.PersistentPreRunE != nil {
						return parent.PersistentPreRunE(selected, args)
					}
					if parent.PersistentPreRun != nil {
						parent.PersistentPreRun(selected, args)
						break
					}
				}
				return nil
			}
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	if len(hooks.BeforePersistent) != 0 || len(hooks.BeforePersistentLocal) != 0 {
		walk(command)
	}
	if len(hooks.AfterPreRun) != 0 {
		preRunE, preRun := command.PreRunE, command.PreRun
		command.PreRunE = func(selected *cobra.Command, args []string) error {
			if preRunE != nil {
				if err := preRunE(selected, args); err != nil {
					return err
				}
			} else if preRun != nil {
				preRun(selected, args)
			}
			return runCommandHooks(hooks.AfterPreRun, selected, args)
		}
	}
}
