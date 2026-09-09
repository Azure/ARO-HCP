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
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/roleassignments"
)

// resourceGroupDeleter handles ordered deletion of resources in a resource group
type resourceGroupDeleter struct {
	resourceGroupName          string
	subscriptionID             string
	credential                 azcore.TokenCredential
	wait                       bool
	dryRun                     bool
	parallelism                int
	retireOwnedRoleAssignments bool
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

	var capture func(context.Context) (retiredRoleAssignments, error)
	if d.retireOwnedRoleAssignments {
		if !d.wait {
			return fmt.Errorf("role assignment retirement requires synchronous resource group deletion")
		}
		capture = func(ctx context.Context) (retiredRoleAssignments, error) {
			return roleassignments.CaptureResourceGroupRetirement(ctx, d.subscriptionID, d.resourceGroupName, d.credential)
		}
	}

	return withRoleAssignmentRetirement(ctx, d.dryRun, capture, func(ctx context.Context) error {
		eng, err := cleanupengine.ResourceGroupOrderedCleanupWorkflow(
			ctx,
			d.resourceGroupName,
			d.subscriptionID,
			d.credential,
			cleanupengine.WorkflowOptions{
				Wait:            d.wait,
				DryRun:          d.dryRun,
				Parallelism:     d.parallelism,
				ContinueOnError: true,
			},
		)
		if err != nil {
			return err
		}
		return eng.Run(ctx)
	})
}

type retiredRoleAssignments interface {
	Cleanup(context.Context) error
}

func withRoleAssignmentRetirement(ctx context.Context, dryRun bool, capture func(context.Context) (retiredRoleAssignments, error), teardown func(context.Context) error) error {
	var retirement retiredRoleAssignments
	if capture != nil {
		var err error
		retirement, err = capture(ctx)
		if err != nil {
			logr.FromContextOrDiscard(ctx).Error(
				err,
				"Skipping owned role-assignment retirement because ownership capture failed",
			)
			retirement = nil
		}
	}
	if err := teardown(ctx); err != nil {
		return err
	}
	if retirement != nil && !dryRun {
		// ARM deletion can complete before the active Graph object disappears.
		// Retry only that state; permission/validation errors and observed
		// restoration races stop immediately.
		var retainedErr error
		err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := retirement.Cleanup(ctx)
			if errors.Is(err, runner.ErrTargetRetained) {
				retainedErr = err
				return false, nil
			}
			return err == nil, err
		})
		if err != nil {
			if retainedErr != nil {
				err = errors.Join(err, retainedErr)
			}
			return fmt.Errorf("retire owned role assignments: %w", err)
		}
	}
	return nil
}
