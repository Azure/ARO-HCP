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

package maestrohelpers

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

// ReadDesireNameReadonlyNodePool is the well-known ReadDesire name the backend
// writes the per-node-pool NodePool mirror under. Consumers look this up via a
// ReadDesireLister.GetForNodePool call.
//
// The value is the lowercased form of
// api.MaestroBundleInternalNameReadonlyHypershiftNodePool — the same derivation
// the writer (create_nodepool_scoped_read_desires_controller.go) uses for its
// desired ReadDesire name.
var ReadDesireNameReadonlyNodePool = strings.ToLower(string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))

// GetCachedNodePoolForNodePool reads the Hypershift NodePool mirror from the
// per-node-pool ReadDesire. The ReadDesire's Status.KubeContent.Raw carries the
// observed NodePool JSON; we decode it directly and return the typed object.
//
// Returns (nil, nil) when:
//   - the ReadDesire has not been created yet (NotFound),
//   - the ReadDesire exists but the kube-applier has not yet observed
//     the target (Status.KubeContent is nil or empty).
//
// Returns a non-nil error only for hard failures: a non-NotFound lister error,
// or unmarshal failure.
func GetCachedNodePoolForNodePool(
	ctx context.Context,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionName, resourceGroupName, clusterName, nodePoolName string,
) (*v1beta1.NodePool, error) {
	readDesire, err := readDesireLister.GetForNodePool(ctx, subscriptionName, resourceGroupName, clusterName, nodePoolName, ReadDesireNameReadonlyNodePool)
	if database.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ReadDesire for NodePool: %w", err))
	}
	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil, nil
	}
	nodePool := &v1beta1.NodePool{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, nodePool); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal NodePool from ReadDesire kubeContent: %w", err))
	}
	return nodePool, nil
}
