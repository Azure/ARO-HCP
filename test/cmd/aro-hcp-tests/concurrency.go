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
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
)

// SuiteConcurrency holds construction-time worker defaults, independently of MI admission.
type suiteConcurrency struct {
	override int
	acquired int
}

// Parse suite worker settings once before registration; an explicitly empty override opts out.
func parseSuiteConcurrency(acquired int) (suiteConcurrency, error) {
	value, supplied := os.LookupEnv("ARO_HCP_SUITE_PARALLELISM")
	if supplied {
		if value == "" {
			return suiteConcurrency{}, nil
		}
		override, err := strconv.Atoi(value)
		if err != nil || override <= 0 {
			return suiteConcurrency{}, fmt.Errorf("ARO_HCP_SUITE_PARALLELISM=%q is not a valid positive integer", value)
		}
		return suiteConcurrency{override: override}, nil
	}
	return suiteConcurrency{acquired: acquired}, nil
}

// Resolve a suite's workers using the user override, explicit lease opt-in, then fallback.
func (workers suiteConcurrency) resolve(fallback int, useAcquired bool) int {
	if workers.override > 0 {
		return workers.override
	}
	if useAcquired && workers.acquired > 0 {
		return workers.acquired
	}
	return fallback
}

// Configure every selected suite's worker count independently of MI resource admission.
func configureSuiteConcurrency(command *cobra.Command, registry *e.Registry) {
	existingPreRunE, existingPreRun := command.PreRunE, command.PreRun
	command.PreRunE = func(c *cobra.Command, args []string) error {
		if existingPreRunE != nil {
			if err := existingPreRunE(c, args); err != nil {
				return err
			}
		} else if existingPreRun != nil {
			existingPreRun(c, args)
		}
		component, err := c.Flags().GetString("component")
		if err != nil {
			return fmt.Errorf("reading run-suite --component: %w", err)
		}
		ext := registry.Get(component)
		if ext == nil {
			return fmt.Errorf("component not found: %s", component)
		}
		if len(args) != 1 {
			return fmt.Errorf("must specify one suite name")
		}
		suite, err := ext.GetSuite(args[0])
		if err != nil {
			return fmt.Errorf("couldn't find suite %q: %w", args[0], err)
		}
		flag := c.Flags().Lookup("max-concurrency")
		if flag == nil {
			return fmt.Errorf("run-suite is missing the expected --max-concurrency flag")
		}
		concurrency := suite.Parallelism
		if flag.Changed {
			value, err := c.Flags().GetInt("max-concurrency")
			if err != nil {
				return fmt.Errorf("reading run-suite --max-concurrency: %w", err)
			}
			// Leave explicit CLI values to the upstream flag and runner validation.
			concurrency = value
		}
		if err := flag.Value.Set(strconv.Itoa(concurrency)); err != nil {
			return fmt.Errorf("setting run-suite --max-concurrency: %w", err)
		}
		// GetSuite returns a copy. Update the registered cap too, because the
		// upstream runner takes min(flag, suite cap), but leave MI pools intact.
		for i := range ext.Suites {
			if ext.Suites[i].Name == suite.Name {
				ext.Suites[i].Parallelism = concurrency
				break
			}
		}
		return nil
	}
}

// Resolve suite workers in explicit CLI, environment, then suite-default order.
func resolveSuiteConcurrency(suiteDefault int, env string, cli *int) (int, error) {
	if cli != nil {
		// Leave explicit CLI values to the upstream flag and runner validation.
		return *cli, nil
	}
	if env == "" {
		return suiteDefault, nil
	}
	concurrency, err := strconv.Atoi(env)
	if err != nil || concurrency <= 0 {
		return 0, fmt.Errorf("ARO_HCP_SUITE_PARALLELISM=%q is not a valid positive integer", env)
	}
	return concurrency, nil
}

// Apply the same environment resolution to metadata without preempting CLI overrides.
func configureSuiteMetadataConcurrency(command *cobra.Command, registry *e.Registry) {
	for _, child := range command.Commands() {
		configureSuiteMetadataConcurrency(child, registry)
	}
	if command.Run == nil && command.RunE == nil {
		return
	}
	existingPreRunE, existingPreRun := command.PreRunE, command.PreRun
	command.PreRunE = func(c *cobra.Command, args []string) error {
		if existingPreRunE != nil {
			if err := existingPreRunE(c, args); err != nil {
				return err
			}
		} else if existingPreRun != nil {
			existingPreRun(c, args)
		}
		env := os.Getenv("ARO_HCP_SUITE_PARALLELISM")
		if env == "" {
			return nil
		}
		concurrency, err := resolveSuiteConcurrency(0, env, nil)
		if err != nil {
			return err
		}
		registry.Walk(func(ext *e.Extension) {
			for i := range ext.Suites {
				ext.Suites[i].Parallelism = concurrency
			}
		})
		return nil
	}
}
