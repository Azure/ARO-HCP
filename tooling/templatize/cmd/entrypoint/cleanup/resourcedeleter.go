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

package cleanup

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/go-logr/logr"

	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
)

// resourceGroupDeleter handles ordered deletion of resources in a resource group
type resourceGroupDeleter struct {
	resourceGroupName string
	subscriptionID    string
	credential        azcore.TokenCredential
	wait              bool
	dryRun            bool
	parallelism       int
}

func (d *resourceGroupDeleter) execute(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get logger from context: %w", err)
	}
	if d.dryRun {
		logger.Info("DRY-RUN MODE - No actual deletions will be performed")
	}
	logger.Info("Starting ordered cleanup workflow")

	eng, err := cleanupengine.ResourceGroupOrderedCleanupWorkflow(
		ctx,
		d.resourceGroupName,
		d.subscriptionID,
		d.credential,
		cleanupengine.WorkflowOptions{
			Wait:        d.wait,
			DryRun:      d.dryRun,
			Parallelism: d.parallelism,
		},
	)
	if err != nil {
		return err
	}

	return eng.Run(ctx)
}
