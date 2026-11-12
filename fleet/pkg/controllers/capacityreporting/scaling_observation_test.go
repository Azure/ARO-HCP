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

package capacityreporting

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

type scalingTransport func(*http.Request) (*http.Response, error)

func (transport scalingTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestScalingSKULookupFailurePreservesScheduling(t *testing.T) {
	for _, test := range []struct {
		name       string
		observed   bool
		configured string
		want       corev1.ResourceList
	}{
		{
			name: "mixed-observed-and-bootstrap", observed: true, configured: "2",
			want: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("12"), corev1.ResourceMemory: resource.MustParse("48Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("11"),
			},
		},
		{name: "configured-NIC-only", configured: "2", want: corev1.ResourceList{kuberesources.SwiftNICResourceName: resource.MustParse("8")}},
		{name: "configured-explicit-zero", configured: "0", want: corev1.ResourceList{kuberesources.SwiftNICResourceName: resource.MustParse("0")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			managementCluster := testManagementCluster()
			database, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{managementCluster})
			require.NoError(t, err)
			report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
				Conditions: []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
			}}
			pools := []armcontainerservice.AgentPool{{Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
				VMSize: ptr.To("Standard_Bootstrap"), Count: ptr.To(int32(1)), EnableAutoScaling: ptr.To(true), MaxCount: ptr.To(int32(4)),
				NodeLabels: workerNodeLabels(), Tags: map[string]*string{agentpoolspec.SwiftSecondaryNICCountTag: ptr.To(test.configured)},
			}}}
			if test.observed {
				report.Status.Nodes = []capacityreportv1alpha1.NodeSKUCapacity{{
					SKU: "Standard_Observed", Ready: 2, Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi"),
						kuberesources.SwiftNICResourceName: resource.MustParse("2"),
					},
				}}
				pools = append(pools, armcontainerservice.AgentPool{Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					VMSize: ptr.To("Standard_Observed"), Count: ptr.To(int32(3)), NodeLabels: workerNodeLabels(),
				}})
			}
			lookupFails, skuCalls := true, 0
			transport := scalingTransport(func(request *http.Request) (*http.Response, error) {
				var body any
				status := http.StatusOK
				switch {
				case strings.HasSuffix(request.URL.Path, "/agentPools"):
					body = map[string]any{"value": pools}
				case strings.HasSuffix(request.URL.Path, "/skus"):
					skuCalls++
					body = map[string]any{"value": []any{map[string]any{"name": "Standard_Bootstrap", "resourceType": "virtualMachines", "capabilities": []map[string]string{{"name": "vCPUs", "value": "2"}, {"name": "MemoryGB", "value": "8"}}}}}
					if lookupFails {
						status = http.StatusForbidden
						body = map[string]any{"error": map[string]string{"code": "AuthorizationFailed", "message": "injected SKU lookup failure"}}
					}
				default:
					t.Fatalf("unexpected Azure request: %s", request.URL.Path)
				}
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request}, nil
			})
			credential := &azfake.TokenCredential{}
			options := &policy.ClientOptions{Transport: transport}
			factory, _ := agentpools.NewClientFactory(credential, options)
			desire := controllerutils.BuildReadDesire(kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(scaleCeilingTestStampIdentifier, ReadDesireName), managementCluster.ResourceID, CapacityReportTarget)
			desire.Status = buildTestReadDesire(report).Status
			syncer := &scaleCeilingReportingSyncer{
				fleetDBClient: database, readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{desire}},
				agentPoolClientFactory: factory, skuCache: skucache.NewSKUCache("eastus", credential, options, nil),
			}
			key := fleetcontrollers.StampKey{StampIdentifier: scaleCeilingTestStampIdentifier}
			crud := database.Stamps().ManagementClusters(key.StampIdentifier).Scheduling()
			require.ErrorContains(t, syncer.SyncOnce(ctx, key), "AuthorizationFailed")
			_, err = crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.True(t, cosmosstorageutils.IsNotFoundError(err), "SKU failure must not create scheduling")
			first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			require.NoError(t, syncer.persistMaxCapacity(ctx, key.StampIdentifier, corev1.ResourceList{kuberesources.SwiftNICResourceName: resource.MustParse("9")}, &first))
			before, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.Positive(t, skuCalls)
			require.ErrorContains(t, syncer.SyncOnce(ctx, key), "AuthorizationFailed")
			stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.Equal(t, before, stable, "SKU failure preserves every field, including ceiling, conditions, timestamps and ETag")
			lookupFails = false
			require.NoError(t, syncer.SyncOnce(ctx, key))
			recovered, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			wantRecovered := test.want.DeepCopy()
			addScaled(wantRecovered, corev1.ResourceCPU, resource.MustParse("2"), 4)
			addScaled(wantRecovered, corev1.ResourceMemory, resource.MustParse("8Gi"), 4)
			assertResourceListEqual(t, wantRecovered, recovered.Status.ScaleCeiling.Capacity, "successful SKU lookup publishes observed and fallback contributions")
			condition := meta.FindStatusCondition(recovered.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent)
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionTrue, condition.Status)
			require.Equal(t, "DataCollected", condition.Reason)
			require.NotEqual(t, before.CosmosETag, recovered.CosmosETag)
		})
	}
}
