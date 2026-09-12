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

package kubeappliercosmosstorage

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// UnknownApplyDesireController is the bucket used for ApplyDesires that carry no
// kubeapplierapi.TagControllerName tag.
const UnknownApplyDesireController = "unknown"

// SummarizeApplyDesiresByController enumerates every ApplyDesire reachable through
// the given CRUD and associates each with its authoring controller (the value
// recorded under Tags[kubeapplierapi.TagControllerName]; ApplyDesires with no such
// tag are bucketed under UnknownApplyDesireController). It returns the total count
// and a stable, human-readable breakdown sorted by controller name — so the message
// is deterministic across reconciles regardless of per-controller counts — e.g.
// "1 for controller SomeController, 2 for controller unknown".
//
// It is the single source of truth for the cluster-deletion gates that must wait
// for ApplyDesires to drain (see the ServiceProviderCluster deletion gate and the
// delete-operation timeout message).
func SummarizeApplyDesiresByController(ctx context.Context, applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]) (int, string, error) {
	applyDesireIterator, err := applyDesireCRUD.List(ctx, &cosmosstorageutils.DBClientListResourceDocsOptions{})
	if err != nil {
		return 0, "", fmt.Errorf("failed to list ApplyDesire documents: %w", err)
	}

	countsByController := map[string]int{}
	total := 0
	for _, desire := range applyDesireIterator.Items(ctx) {
		controllerName := desire.Tags[kubeapplierapi.TagControllerName]
		if controllerName == "" {
			controllerName = UnknownApplyDesireController
		}
		countsByController[controllerName]++
		total++
	}
	if err := applyDesireIterator.GetError(); err != nil {
		return 0, "", fmt.Errorf("error iterating ApplyDesire documents: %w", err)
	}

	// Sort by controller name (the map key) before formatting so the breakdown is
	// stable across reconciles instead of depending on per-controller counts.
	controllerNames := make([]string, 0, len(countsByController))
	for controllerName := range countsByController {
		controllerNames = append(controllerNames, controllerName)
	}
	slices.Sort(controllerNames)

	parts := make([]string, 0, len(controllerNames))
	for _, controllerName := range controllerNames {
		parts = append(parts, fmt.Sprintf("%d for controller %s", countsByController[controllerName], controllerName))
	}
	return total, strings.Join(parts, ", "), nil
}
