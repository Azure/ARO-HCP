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

package kubeapplierhelpers

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// EnsureMatchingApplyDesiresRemoved is the teardown primitive the removal steps share:
// it flips every desire in owned that matches selects to Type=Delete and purges
// the ones the kube-applier reports as deleted. owned must already be filtered
// to the desires this controller owns. done is true only when nothing
// matching selects is left.
func EnsureMatchingApplyDesiresRemoved(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	stepName string,
	owned []*kubeapplierapi.ApplyDesire,
	selects func(*kubeapplierapi.ApplyDesire) bool,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	var waitingFor []string
	var errs []error
	for _, desire := range owned {
		if !selects(desire) {
			continue
		}

		desireName := desire.ResourceID.Name
		scope, err := kubeappliercosmosstorage.ParseDesireScope(desire.ResourceID.Parent)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		crud, err := kubeApplierDBClient.ApplyDesiresFor(scope)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		removed, err := EnsureApplyDesireRemoved(ctx, desireName, crud)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !removed {
			waitingFor = append(waitingFor, desireName)
			continue
		}
		logger.Info("deleted ApplyDesire", "step", stepName, "desireName", desireName)

		// Now that the ApplyDesire is removed and the object is gone from the
		// management cluster, delete the corresponding ReadDesire. It was only
		// needed for observability during deletion; now that deletion is complete,
		// it's no longer useful.
		//
		// ReadDesire deletion errors are fatal: if we log and continue, the step
		// reports done=true while the ReadDesire is still live. The cleanup gate
		// preserves ClusterResources-owned ReadDesires and ServiceProviderCluster
		// deletion waits for every desire, so a transient Cosmos failure here would
		// permanently block cluster deletion. Return the error so the step stays
		// incomplete and retries on the next reconcile.
		readDesireCRUD, readErr := kubeApplierDBClient.ReadDesiresFor(scope)
		if readErr != nil {
			return false, readErr
		}
		if delErr := readDesireCRUD.Delete(ctx, desireName); delErr != nil && !cosmosstorageutils.IsNotFoundError(delErr) {
			return false, utils.TrackError(fmt.Errorf("delete ReadDesire %s: %w", desireName, delErr))
		}
		logger.Info("deleted ReadDesire", "step", stepName, "desireName", desireName)
	}

	// Report errors ahead of the wait list: a step that could not be fully
	// processed must not be treated as drained, so the chain stops here.
	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	if len(waitingFor) > 0 {
		logger.Info("ApplyDesire teardown in progress",
			"step", stepName, "waitingFor", waitingFor)
		return false, nil
	}

	return true, nil
}
