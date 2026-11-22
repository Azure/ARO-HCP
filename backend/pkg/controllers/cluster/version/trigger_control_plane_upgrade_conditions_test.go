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

package version

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func upgradeTestKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
}

func upgradeReadDesires(t *testing.T, eligible bool) []*kubeapplierapi.ReadDesire {
	t.Helper()
	status, reason := metav1.ConditionTrue, "AsExpected"
	if !eligible {
		status, reason = metav1.ConditionFalse, "OperatorBlocked"
	}
	hc := &hsv1beta1.HostedCluster{
		TypeMeta:   metav1.TypeMeta{APIVersion: hsv1beta1.SchemeGroupVersion.String(), Kind: "HostedCluster"},
		ObjectMeta: metav1.ObjectMeta{Name: "domain-prefix", Namespace: "ocm-arohcpint-test-cluster"},
		Status: hsv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{
			{Type: string(hsv1beta1.ReconciliationActive), Status: metav1.ConditionTrue},
			{Type: string(hsv1beta1.ClusterVersionUpgradeable), Status: status, Reason: reason, Message: "original operator message"},
		}},
	}
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	mcID := metadataapi.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/1/managementclusters/upgrade-test"))
	return []*kubeapplierapi.ReadDesire{{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: hostedClusterReadDesireResourceID(t), PartitionKey: mcID.String()},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: mcID,
			TargetItem:        kubeapplierapi.ResourceReference{Group: hsv1beta1.SchemeGroupVersion.Group, Version: "v1beta1", Resource: "hostedclusters", Namespace: hc.Namespace, Name: hc.Name},
		},
		Status: kubeapplierapi.ReadDesireStatus{KubeContent: &kruntime.RawExtension{Raw: raw}},
	}}
}

func newUpgradeTestSyncer(t *testing.T, client cincinnati.Client) (*triggerControlPlaneUpgradeSyncer, *coreapi.Cluster, *coreapi.ServiceProviderCluster) {
	t.Helper()
	clusterID := metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	cluster := &coreapi.Cluster{
		CosmosMetadata:            coreapi.CosmosMetadata{ResourceID: clusterID},
		TrackedResource:           coreapi.TrackedResource{Resource: coreapi.NewResource(clusterID)},
		CustomerProperties:        coreapi.ClusterCustomerProperties{Version: coreapi.VersionProfile{ChannelGroup: "stable"}},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterServiceID: ptr.To(metadataapi.Must(metadataapi.NewInternalID(testCSClusterIDStr)))},
	}
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(coreapihelpers.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName))), PartitionKey: testSubscriptionID},
		Spec:           coreapi.ServiceProviderClusterSpec{ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{DesiredVersion: ptr.To(semver.MustParse("4.20.2"))}},
		Status:         coreapi.ServiceProviderClusterStatus{ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{ActiveVersions: []coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.1")), State: configv1.CompletedUpdate}}}},
	}
	cache := cincinnati.NewMockClientCache(gomock.NewController(t))
	cache.EXPECT().GetOrCreateClient(gomock.Any()).Return(client).AnyTimes()
	return &triggerControlPlaneUpgradeSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)),
		cincinnatiClientCache:        cache,
		ocpImagesACRHostname:         "ocp.example.azurecr.io",
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}},
		activeOperationLister:        &corelistertesting.SliceActiveOperationLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{Desires: upgradeReadDesires(t, true)},
	}, cluster, spc
}
