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

package admission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestAdmitClusterCapacity(t *testing.T) {
	swiftSubnet := metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestVnetIntegrationSubnetResourceID))
	cluster := func(sizing coreapi.ControlPlanePodSizing, swiftSubnet *azcorearm.ResourceID) *coreapi.Cluster {
		c := &coreapi.Cluster{}
		c.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing = sizing
		c.CustomerProperties.Platform.VnetIntegrationSubnetID = swiftSubnet
		return c
	}
	failed := cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet)
	failed.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateFailed
	provisioning := cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet)
	provisioning.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateProvisioning
	deleting := cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet)
	deleting.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateDeleting
	deleting.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{}

	for _, tt := range []struct {
		name     string
		clusters []*coreapi.Cluster
		limit    int
		newObj   *coreapi.Cluster
		wantErr  string
	}{
		{
			name:   "empty inventory is admitted",
			limit:  2,
			newObj: cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
		},
		{
			name: "no limit admits any inventory",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			},
			newObj: cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
		},
		{
			name: "one below the limit is admitted",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			},
			limit:  2,
			newObj: cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
		},
		{
			name: "at the limit is rejected",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			},
			limit:   2,
			newObj:  cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			wantErr: e2eMinimalSwiftCapacityExhaustedMessage,
		},
		{
			name: "above the limit is rejected",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			},
			limit:   2,
			newObj:  cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			wantErr: e2eMinimalSwiftCapacityExhaustedMessage,
		},
		{
			name: "default sized cluster does not count",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.DefaultControlPlanePodSizing, swiftSubnet),
			},
			limit:  2,
			newObj: cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
		},
		{
			name: "cluster without SWIFT does not count",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, nil),
			},
			limit:  2,
			newObj: cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
		},
		{
			name: "failed cluster counts",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				failed,
			},
			limit:   2,
			newObj:  cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			wantErr: e2eMinimalSwiftCapacityExhaustedMessage,
		},
		{
			name: "provisioning cluster counts",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				provisioning,
			},
			limit:   2,
			newObj:  cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			wantErr: e2eMinimalSwiftCapacityExhaustedMessage,
		},
		{
			name: "deleting cluster counts",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				deleting,
			},
			limit:   2,
			newObj:  cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			wantErr: e2eMinimalSwiftCapacityExhaustedMessage,
		},
		{
			name: "default sized create is admitted at the limit",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			},
			limit:  2,
			newObj: cluster(coreapi.DefaultControlPlanePodSizing, swiftSubnet),
		},
		{
			name: "create without SWIFT is admitted at the limit",
			clusters: []*coreapi.Cluster{
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
				cluster(coreapi.MinimalControlPlanePodSizing, swiftSubnet),
			},
			limit:  2,
			newObj: cluster(coreapi.MinimalControlPlanePodSizing, nil),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			admissionContext := &ClusterCapacityAdmissionContext{
				Clusters:                   tt.clusters,
				MaxE2EMinimalSwiftClusters: tt.limit,
			}
			err := AdmitClusterCapacity(context.Background(), admissionContext, tt.newObj)
			if len(tt.wantErr) == 0 {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}
