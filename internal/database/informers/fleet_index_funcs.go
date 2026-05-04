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

package informers

import (
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func managementClusterProvisionShardIDIndexFunc(obj interface{}) ([]string, error) {
	mc, ok := obj.(*fleet.ManagementCluster)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("expected *fleet.ManagementCluster, got %T", obj))
	}
	if mc.Status.ClusterServiceProvisionShardID == nil || len(mc.Status.ClusterServiceProvisionShardID.ID()) == 0 {
		return nil, nil
	}
	return []string{mc.Status.ClusterServiceProvisionShardID.ID()}, nil
}
