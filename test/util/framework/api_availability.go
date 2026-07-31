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

package framework

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// APIVersionAvailable probes the cluster list endpoint using specific API version to determine API version availability
func (tc *perItOrDescribeTestContext) APIVersionAvailable(ctx context.Context, resourceGroupName string, version metadataapi.APIVersion) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	var err error
	switch version {
	case metadataapi.APIVersionV20240610Preview:
		client, clientErr := tc.Get20240610ClientFactory(probeCtx)
		if clientErr != nil {
			return false, clientErr
		}
		_, err = client.NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(resourceGroupName, nil).NextPage(probeCtx)
	case metadataapi.APIVersionV20251223Preview:
		client, clientErr := tc.Get20251223ClientFactory(probeCtx)
		if clientErr != nil {
			return false, clientErr
		}
		_, err = client.NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(resourceGroupName, nil).NextPage(probeCtx)
	case metadataapi.APIVersionV20260630Preview:
		client, clientErr := tc.Get20260630ClientFactory(probeCtx)
		if clientErr != nil {
			return false, clientErr
		}
		_, err = client.NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(resourceGroupName, nil).NextPage(probeCtx)
	default:
		return false, fmt.Errorf("unsupported API version %s", version)
	}

	// cluster list was successful -> API version is available
	if err == nil {
		return true, nil
	}

	if IsAPINotDeployedError(err) {
		return false, nil
	}

	return false, fmt.Errorf("failed to check availability of API version %s: %w", version, err)
}
