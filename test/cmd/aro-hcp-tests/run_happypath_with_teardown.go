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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// NewRunHappyPathWithValidationsCommand runs setup-validation, happy-path, and teardown-validation specs with a unified JUnit writer
func NewRunHappyPathWithValidationsCommand() *cobra.Command {
	var junitPath string
	var parallelism int

	cmd := &cobra.Command{
		Use:   "run-happypath-with-validations",
		Short: "Run setup-validation, happy-path, and teardown-validation with a single JUnit report",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancelCause := context.WithCancelCause(context.Background())
			defer cancelCause(errors.New("exiting"))

			abortCh := make(chan os.Signal, 2)
			go func() {
				<-abortCh
				fmt.Fprintf(os.Stderr, "Interrupted, terminating tests")
				cancelCause(errors.New("interrupt received"))

				select {
				case sig := <-abortCh:
					fmt.Fprintf(os.Stderr, "Interrupted twice, exiting (%s)", sig)
					switch sig {
					case syscall.SIGINT:
						os.Exit(130)
					default:
						os.Exit(130) // if we were interrupted, never return zero.
					}

				case <-time.After(30 * time.Minute): // allow time for cleanup.  If we finish before this, we'll exit
					fmt.Fprintf(os.Stderr, "Timed out during cleanup, exiting")
					os.Exit(130) // if we were interrupted, never return zero.
				}
			}()
			signal.Notify(abortCh, syscall.SIGINT, syscall.SIGTERM)

			// Build all specs (Ginkgo discovery)
			specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
			if err != nil {
				return fmt.Errorf("couldn't build extension test specs from ginkgo: %w", err)
			}

			// Shared writers
			composite := et.NewCompositeResultWriter()
			if junitPath != "" {
				junitWriter, err := et.NewJUnitResultWriter(junitPath, "per-run-cluster-tests")
				if err != nil {
					return fmt.Errorf("couldn't create junit writer: %w", err)
				}
				composite.AddWriter(junitWriter)
			}
			// Also echo JSON to stdout for debugging/visibility
			if jsonWriter, jwErr := et.NewJSONResultWriter(os.Stdout, et.ResultFormat("json")); jwErr == nil {
				composite.AddWriter(jsonWriter)
			} else {
				fmt.Fprintf(os.Stderr, "failed to create JSON writer: %v\n", jwErr)
			}

			tc := framework.NewTestContext()
			defer tc.RunCleanupAndDebugWithDefer(ctx)()
			// Use framework's invocation context for resource group creation
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-bicep", tc.Location())
			if err != nil {
				return fmt.Errorf("failed to create resource group: %w", err)
			}
			// Create a cluster using bicep templates
			e2eSetup, err := integration.CreateClusterWithBicep(
				ctx,
				"cluster-only.json",
				*resourceGroup.Name,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				tc.Get20240610ClientFactoryOrDie(ctx),
			)
			if err != nil {
				return fmt.Errorf("failed to create cluster with bicep: %w", err)
			}

			// Prepare selections
			specsHappyPath := specs.Select(et.HasLabel(labels.RequireHappyPathInfra[0]))
			specsValidation := specs.Select(et.HasLabel(labels.TeardownValidation[0]))

			// Run happy-path specs
			if runErr := specsHappyPath.Run(ctx, composite, parallelism); runErr != nil {
				fmt.Fprintf(os.Stderr, "happy-path specs had failures: %v\n", runErr)
			}

			// Cleanup created cluster with a timeout context after happy-path specs
			cleanupTimeout := 60 * time.Minute
			err = framework.DeleteHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				e2eSetup.Cluster.Name,
				cleanupTimeout,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to cleanup HCP cluster: %v\n", err)
			}
			// Run teardown validation specs after HCP cluster deletion
			if runErr := specsValidation.Run(ctx, composite, parallelism); runErr != nil {
				fmt.Fprintf(os.Stderr, "teardown validation specs had failures: %v\n", runErr)
			}

			if flErr := composite.Flush(); flErr != nil {
				fmt.Fprintf(os.Stderr, "failed to flush results: %v\n", flErr)
			}

			return err
		},
	}

	cmd.Flags().StringVarP(&junitPath, "junit-path", "j", junitPath, "write combined results to junit XML")
	cmd.Flags().IntVar(&parallelism, "parallelism", 15, "max parallel tests")
	return cmd
}
