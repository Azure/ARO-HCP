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
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/mipool"
)

// registerMIPool wires the parent-side MI container allocator into the
// run-suite process. Before each child test is spawned, the containers it
// needs are allocated from the pool and handed over through
// ASSIGNED_MI_CONTAINERS; after the child exits they are returned to the pool.
//
// It is a no-op outside the run-suite parent process, when pooled identities
// are disabled, or when no explicit container list is leased.
func registerMIPool(ctx context.Context, specs et.ExtensionTestSpecs, pooledIdentitiesEnabled bool) error {
	if !isRunSuiteProcess() || !pooledIdentitiesEnabled {
		return nil
	}
	containers := strings.TrimSpace(os.Getenv(leaseenv.MSIContainers))
	if containers == "" {
		return nil
	}

	pool, err := mipool.New(containers)
	if err != nil {
		return fmt.Errorf("failed to build MI container pool from %s: %w", leaseenv.MSIContainers, err)
	}

	creds, subscriptionID, err := framework.ParentAzureAccess(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve Azure access for MI container pool: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("component", "mipool")
	identityNames := framework.NewDefaultIdentities().ToSlice()

	// Sweep residual FICs and role assignments left behind by crashed prior runs.
	if err := mipool.CleanupAllContainers(ctx, logger, creds, subscriptionID, pool.AllContainers(), identityNames); err != nil {
		return err
	}

	cleanup, err := newMIContainerCleaner(ctx, logger, creds, subscriptionID, identityNames)
	if err != nil {
		return err
	}

	wireMIPool(specs, pool, cleanup)
	return nil
}

// wireMIPool registers the allocate-before-spawn and release-after-test hooks
// for every spec that declares an MIContainers:N demand.
func wireMIPool(specs et.ExtensionTestSpecs, pool *mipool.Pool, cleanup func(resourceGroup string) error) {
	demands := map[string]int{}
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		if demand, ok := parseMIContainersLabel(spec); ok && demand > 0 {
			demands[spec.Name] = demand
		}
	})

	allocate := func(name string) (string, bool) {
		demand := demands[name]
		if demand == 0 {
			return "", false
		}
		if _, err := pool.Allocate(name, demand); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to allocate %d MI containers for test %q, the test will fail without them: %v\n", demand, name, err)
			return "", false
		}
		return pool.FormatEnv(name), true
	}

	specs.AddBeforeSpawn(func(name string, options *et.SpawnOptions) {
		env, ok := allocate(name)
		if !ok {
			return
		}
		if options.Env == nil {
			options.Env = map[string]string{}
		}
		options.Env[mipool.AssignedMIContainersEnvvar] = env
	})

	// A suite that selects exactly one spec runs it in-process in the parent,
	// so BeforeSpawn never fires. In that case the parent is also the worker:
	// allocate here and hand the containers over through its own environment.
	specs.AddBeforeEach(func(spec et.ExtensionTestSpec) {
		if os.Getenv(et.SpawnedChildEnv) == "1" || len(pool.Allocated(spec.Name)) > 0 {
			return
		}
		if env, ok := allocate(spec.Name); ok {
			os.Setenv(mipool.AssignedMIContainersEnvvar, env)
		}
	})

	// Release hooks are registered per spec because the result passed to
	// AfterEach is not guaranteed to carry the test name.
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		name := spec.Name
		if demands[name] == 0 {
			return
		}
		et.ExtensionTestSpecs{spec}.AddAfterEach(func(res *et.ExtensionTestResult) {
			if len(pool.Allocated(name)) == 0 {
				return
			}
			// A passing child already cleaned up its own FICs and role assignments.
			// Only a failed child may have skipped that cleanup, so the parent does it.
			var err error
			if res.Result == et.ResultPassed {
				err = pool.Release(name)
			} else {
				err = pool.ReleaseWithCleanup(name, cleanup)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: failed to release MI containers for test %q: %v\n", name, err)
			}
		})
	})
}

func newMIContainerCleaner(
	ctx context.Context,
	logger *slog.Logger,
	creds azcore.TokenCredential,
	subscriptionID string,
	identityNames []string,
) (func(resourceGroup string) error, error) {
	msiClientFactory, err := armmsi.NewClientFactory(subscriptionID, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create MSI client factory: %w", err)
	}
	ficsClient := msiClientFactory.NewFederatedIdentityCredentialsClient()
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %w", err)
	}
	return func(resourceGroup string) error {
		return mipool.CleanupContainer(ctx, logger.With("resourceGroup", resourceGroup), ficsClient, roleAssignmentsClient, subscriptionID, resourceGroup, identityNames)
	}, nil
}
