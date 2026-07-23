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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/json"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler is the well-known ReadDesire name the
// backend writes the per-cluster cluster-autoscaler ControlPlaneComponent mirror under.
var ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler = strings.ToLower(string(api.ReadonlyHypershiftControlPlaneComponentClusterAutoscaler))

// GetCachedControlPlaneClusterAutoscalerForCluster reads the cluster-autoscaler
// ControlPlaneComponent mirror from the per-cluster ReadDesire.
func GetCachedControlPlaneClusterAutoscalerForCluster(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName string,
) (*v1beta1.ControlPlaneComponent, error) {
	readDesire, err := readDesireLister.GetForCluster(ctx, subscriptionName, resourceGroupName, clusterName, ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for cluster-autoscaler ControlPlaneComponent: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	controlPlaneComponent := &v1beta1.ControlPlaneComponent{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, controlPlaneComponent); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal ControlPlaneComponent from ReadDesire kubeContent: %w", err))
	}
	return controlPlaneComponent, nil
}
