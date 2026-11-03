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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

type scalingTransport func(*http.Request) (*http.Response, error)

func (transport scalingTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestScalingObservationFallbackAndRecovery(t *testing.T) {
	for _, scenario := range []string{"observed", "bootstrap", "partial-dimensions", "partial-pools", "no-pools", "unknown-pools", "missing-report", "absent-report", "failed-report", "degraded-report", "malformed-report", "noncurrent-report", "azure-error", "sku-error"} {
		for _, prior := range []string{"none", "zero", "positive"} {
			t.Run(scenario+"/"+prior, func(t *testing.T) {
				ctx := t.Context()
				managementCluster := testManagementCluster()
				database, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{managementCluster})
				require.NoError(t, err)
				lister := &kubeapplierlistertesting.SliceReadDesireLister{}
				report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
					Conditions: []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
					Nodes:      []capacityreportv1alpha1.NodeSKUCapacity{{SKU: "Standard_Test", Ready: 2, Allocatable: withZeroFloor(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi")})}},
				}}
				if scenario == "bootstrap" || scenario == "sku-error" {
					report.Status.Nodes = nil
				}
				if scenario == "partial-dimensions" {
					report.Status.Nodes[0].Allocatable = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}
				}
				if scenario == "noncurrent-report" {
					report.Status.Conditions[0].Status = metav1.ConditionFalse
				}
				desire := controllerutils.BuildReadDesire(kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(scaleCeilingTestStampIdentifier, ReadDesireName), managementCluster.ResourceID, CapacityReportTarget)
				desire.Status = buildTestReadDesire(report).Status
				switch scenario {
				case "absent-report":
					desire.Status.KubeContent = nil
				case "failed-report":
					desire.Status.Conditions[0].Status = metav1.ConditionFalse
				case "degraded-report":
					desire.Status.Conditions = append(desire.Status.Conditions, metav1.Condition{Type: kubeapplierapi.ConditionTypeDegraded, Status: metav1.ConditionTrue})
				case "malformed-report":
					desire.Status.KubeContent = malformedContent()
				}
				if scenario != "missing-report" {
					lister.Desires = []*kubeapplierapi.ReadDesire{desire}
				}
				phase, skuCalls := "initial", 0
				transport := scalingTransport(func(request *http.Request) (*http.Response, error) {
					var body any
					status := http.StatusOK
					switch {
					case strings.HasSuffix(request.URL.Path, "/agentPools"):
						pool := armcontainerservice.AgentPool{Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{VMSize: ptr.To("Standard_Test"), Count: ptr.To(int32(2)), NodeLabels: workerNodeLabels()}}
						pools := []armcontainerservice.AgentPool{pool}
						if phase == "empty" || (phase == "initial" && scenario == "no-pools") {
							pools = nil
						}
						if phase == "initial" && scenario == "unknown-pools" {
							pools = []armcontainerservice.AgentPool{{}}
						}
						if phase == "initial" && scenario == "partial-pools" {
							pools = append(pools, armcontainerservice.AgentPool{})
						}
						body = map[string]any{"value": pools}
						if phase == "initial" && scenario == "azure-error" {
							status = http.StatusForbidden
						}
					case strings.HasSuffix(request.URL.Path, "/skus"):
						skuCalls++
						body = map[string]any{"value": []any{map[string]any{"name": "Standard_Test", "resourceType": "virtualMachines", "capabilities": []map[string]string{{"name": "vCPUs", "value": "4"}, {"name": "MemoryGB", "value": "16"}}}}}
						if phase == "initial" && scenario == "sku-error" {
							status = http.StatusForbidden
						}
					default:
						t.Fatalf("unexpected Azure request: %s", request.URL.Path)
					}
					if status != http.StatusOK {
						body = map[string]any{"error": map[string]string{"code": "AuthorizationFailed", "message": "injected refresh failure"}}
					}
					encoded, err := json.Marshal(body)
					require.NoError(t, err)
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request}, nil
				})
				credential := &azfake.TokenCredential{}
				options := &policy.ClientOptions{Transport: transport}
				factory, _ := agentpools.NewClientFactory(credential, options)
				syncer := &scaleCeilingReportingSyncer{fleetDBClient: database, readDesireLister: lister, agentPoolClientFactory: factory, skuCache: skucache.NewSKUCache("eastus", credential, options, nil)}
				key := fleetcontrollers.StampKey{StampIdentifier: scaleCeilingTestStampIdentifier}
				crud := database.Stamps().ManagementClusters(scaleCeilingTestStampIdentifier).Scheduling()
				var before *fleetapi.ManagementClusterScheduling
				if prior != "none" {
					capacity := withZeroFloor(nil)
					if prior == "positive" {
						capacity[corev1.ResourceCPU] = resource.MustParse("20")
					}
					first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
					require.NoError(t, syncer.persistMaxCapacity(ctx, key.StampIdentifier, capacity, &first))
					before, err = crud.Get(ctx, fleetapi.SchedulingResourceName)
					require.NoError(t, err)
				}
				require.NoError(t, syncer.SyncOnce(ctx, key))
				result, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
				require.NoError(t, err)
				success := scenario == "observed" || scenario == "bootstrap" || scenario == "partial-dimensions" || scenario == "partial-pools" || scenario == "no-pools"
				if success {
					require.True(t, meta.IsStatusConditionTrue(result.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent))
					if scenario == "no-pools" {
						assertResourceListEqual(t, withZeroFloor(nil), result.Status.ScaleCeiling.Capacity, "confirmed empty")
					} else {
						require.Equal(t, int64(8), result.Status.ScaleCeiling.Capacity.Cpu().Value())
					}
					if scenario == "partial-dimensions" {
						require.Len(t, result.Status.ScaleCeiling.Capacity, 1)
					}
					if scenario == "bootstrap" {
						require.Equal(t, "BootstrapEstimate", meta.FindStatusCondition(result.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent).Reason)
						_, present := result.Status.ScaleCeiling.Capacity[kuberesources.SwiftNICResourceName]
						require.False(t, present)
					}
				} else if before != nil {
					require.Equal(t, before, result, "failed refresh must not change any persisted field, including explicit zero")
				} else {
					require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(result.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent).Status)
					require.Empty(t, result.Status.ScaleCeiling.Capacity)
				}
				if scenario != "bootstrap" && scenario != "sku-error" {
					require.Zero(t, skuCalls, "must not look up hardware without bootstrap evidence")
				} else {
					require.Positive(t, skuCalls)
				}
				require.NoError(t, syncer.SyncOnce(ctx, key))
				stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
				require.NoError(t, err)
				require.Equal(t, result.CosmosETag, stable.CosmosETag)
				phase = "recovered"
				report.Status.Conditions[0].Status = metav1.ConditionTrue
				report.Status.Nodes = []capacityreportv1alpha1.NodeSKUCapacity{{SKU: "Standard_Test", Ready: 1, Allocatable: withZeroFloor(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")})}}
				desire.Status = buildTestReadDesire(report).Status
				lister.Desires = []*kubeapplierapi.ReadDesire{desire}
				require.NoError(t, syncer.SyncOnce(ctx, key))
				recovered, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
				require.NoError(t, err)
				require.True(t, meta.IsStatusConditionTrue(recovered.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent))
				require.Equal(t, int64(2), recovered.Status.ScaleCeiling.Capacity.Cpu().Value(), "successful reductions replace retained capacity")
				phase = "empty"
				lister.Desires = nil
				require.NoError(t, syncer.SyncOnce(ctx, key))
				empty, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
				require.NoError(t, err)
				assertResourceListEqual(t, withZeroFloor(nil), empty.Status.ScaleCeiling.Capacity, "successful empty replaces previous capacity without a report")
			})
		}
	}
}
