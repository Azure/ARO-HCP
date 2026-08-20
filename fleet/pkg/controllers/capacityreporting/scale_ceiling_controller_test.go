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
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

const scaleCeilingTestStampIdentifier = "s1"

func testManagementCluster() *fleetapi.ManagementCluster {
	resourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID(scaleCeilingTestStampIdentifier))
	aksResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"))
	dnsResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"))
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(scaleCeilingTestStampIdentifier),
		},
		ResourceID: resourceID,
		Spec: fleetapi.ManagementClusterSpec{
			SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleetapi.ManagementClusterStatus{
			AKSResourceID:                                        aksResourceID,
			PublicDNSZoneResourceID:                              dnsResourceID,
			HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			MaestroConsumerName:                                  "consumer-1",
			MaestroRESTAPIURL:                                    "http://maestro:8000",
			MaestroGRPCTarget:                                    "maestro:8090",
			KubeApplierCosmosContainerName:                       "kube-applier-test",
		},
	}
}

func testSchedulingDoc() *fleetapi.ManagementClusterScheduling {
	return &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(scaleCeilingTestStampIdentifier)),
			PartitionKey: strings.ToLower(scaleCeilingTestStampIdentifier),
		},
	}
}

func TestPersistMaxCapacity_NotFoundSchedulingDoc(t *testing.T) {
	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()

	syncer := &scaleCeilingReportingSyncer{
		fleetDBClient: fleetDB,
	}

	now := metav1.Now()
	err := syncer.persistMaxCapacity(context.Background(), scaleCeilingTestStampIdentifier, corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("100Gi"),
	}, &now)
	require.NoError(t, err)
}

func TestPersistMaxCapacity_UpdatesExistingDoc(t *testing.T) {
	ctx := context.Background()
	fleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster()})
	require.NoError(t, err)

	schedulingCRUD := fleetDB.Stamps().ManagementClusters(scaleCeilingTestStampIdentifier).Scheduling()
	_, err = schedulingCRUD.Create(ctx, testSchedulingDoc(), nil)
	require.NoError(t, err)

	syncer := &scaleCeilingReportingSyncer{
		fleetDBClient: fleetDB,
	}

	maxCapacity := corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("256Gi"),
	}
	now := metav1.Now()
	err = syncer.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, maxCapacity, &now)
	require.NoError(t, err)

	updated, err := schedulingCRUD.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	assertResourceListEqual(t, maxCapacity, updated.Status.Scaling.Max, "Scaling.Max")
	require.NotNil(t, updated.Status.Scaling.LastReportedAt, "Scaling.LastReportedAt must be set")
	assert.True(t, meta.IsStatusConditionTrue(updated.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent), "ScalingDataCurrent condition")
}

func TestPersistMaxCapacity_PreservesCapacity(t *testing.T) {
	ctx := context.Background()
	fleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster()})
	require.NoError(t, err)

	scheduling := testSchedulingDoc()
	scheduling.Status.Capacity = fleetapi.ManagementClusterCapacity{
		Current: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("64Gi"),
		},
	}

	schedulingCRUD := fleetDB.Stamps().ManagementClusters(scaleCeilingTestStampIdentifier).Scheduling()
	_, err = schedulingCRUD.Create(ctx, scheduling, nil)
	require.NoError(t, err)

	syncer := &scaleCeilingReportingSyncer{
		fleetDBClient: fleetDB,
	}

	now := metav1.Now()
	err = syncer.persistMaxCapacity(ctx, scaleCeilingTestStampIdentifier, corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("256Gi"),
	}, &now)
	require.NoError(t, err)

	updated, err := schedulingCRUD.Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	assertResourceListEqual(t, corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Gi")}, updated.Status.Capacity.Current, "Capacity.Current preserved")
	assertResourceListEqual(t, corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Gi")}, updated.Status.Scaling.Max, "Scaling.Max updated")
}
