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

package istio

import (
	"context"
	"fmt"
	"regexp"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var gatewayRevisionLeasePattern = regexp.MustCompile(
	`^istio-gateway-(?:deployment|status-leader)-(asm-\d+-\d+)$`,
)

// ReconcileOrphanedGatewayLeases removes AKS-managed Istio gateway
// leader-election leases only when their revision is no longer installed.
// The caller must confirm the mesh is stable before invoking it.
//
// Requires list/delete on coordination.k8s.io/leases in aks-istio-system.
// List failure is returned to the caller. Per-lease delete errors are logged
// and otherwise ignored; NotFound is treated as benign.
func ReconcileOrphanedGatewayLeases(
	ctx context.Context,
	logger logr.Logger,
	kubeClient *KubeClient,
	installedRevisions []string,
) error {
	installed := sets.New(installedRevisions...)

	leases, err := kubeClient.client.CoordinationV1().
		Leases(istioSystemNamespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Istio gateway leader-election leases: %w", err)
	}

	var orphaned []string
	for _, lease := range leases.Items {
		matches := gatewayRevisionLeasePattern.FindStringSubmatch(lease.Name)
		if matches == nil {
			continue
		}

		revision := matches[1]
		if installed.Has(revision) {
			continue
		}

		if err := kubeClient.client.CoordinationV1().
			Leases(istioSystemNamespace).
			Delete(ctx, lease.Name, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			logger.Error(
				err,
				"Failed to delete orphaned Istio gateway leader-election lease (non-fatal)",
				"lease", lease.Name,
			)
			continue
		}
		orphaned = append(orphaned, lease.Name)
	}

	if len(orphaned) > 0 {
		logger.Info("Removed orphaned Istio gateway leases", "count", len(orphaned), "leases", orphaned)
	}

	return nil
}
