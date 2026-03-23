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

package resourcegroup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"k8s.io/apimachinery/pkg/util/sets"

	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
	cleanuprunner "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

type RunOptions struct {
	SubscriptionID  string
	AzureCredential azcore.TokenCredential

	DryRun      bool
	Wait        bool
	Parallelism int

	DiscoverResourceGroups bool
	ResourceGroups         sets.Set[string]
	FailOnDiscoveryError   bool
	ReferenceTime          time.Time

	Policy policy.RGOrderedPolicy
}

func Run(ctx context.Context, opts RunOptions) error {
	logger := cleanuprunner.LoggerFromContext(ctx)

	resourceGroups, err := discoverCandidates(ctx, opts)
	if err != nil {
		return err
	}
	if len(resourceGroups) == 0 {
		logger.Info("No candidate resource groups found for rg-ordered workflow")
		return nil
	}

	logger.Info("Executing rg-ordered workflow", "resourceGroups", len(resourceGroups))
	runErrors := make([]error, 0)

	for _, resourceGroupName := range resourceGroups {
		rgLogger := logger.WithValues("resourceGroup", resourceGroupName)
		rgCtx := cleanuprunner.ContextWithLogger(ctx, rgLogger)

		workflow, err := cleanupengine.ResourceGroupOrderedCleanupWorkflow(
			rgCtx,
			resourceGroupName,
			opts.SubscriptionID,
			opts.AzureCredential,
			cleanupengine.WorkflowOptions{
				DryRun:      opts.DryRun,
				Wait:        opts.Wait,
				Parallelism: opts.Parallelism,
			},
		)
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("failed building rg-ordered workflow: %w", err))
			continue
		}

		rgLogger.Info("Running rg-ordered cleanup")
		if err := workflow.Run(rgCtx); err != nil {
			runErrors = append(runErrors, fmt.Errorf("rg-ordered cleanup failed: %w", err))
			continue
		}
		rgLogger.Info("Finished rg-ordered cleanup")
	}

	return errors.Join(runErrors...)
}
