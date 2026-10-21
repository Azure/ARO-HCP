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
	"context"
	"encoding/json"
	"errors"
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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
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

func newUpgradeTestSyncer(t *testing.T, client ocm.ClusterServiceClientSpec) (*triggerControlPlaneUpgradeSyncer, *coreapi.Cluster, *coreapi.ServiceProviderCluster) {
	t.Helper()
	clusterID := metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	cluster := &coreapi.Cluster{
		CosmosMetadata:            coreapi.CosmosMetadata{ResourceID: clusterID},
		TrackedResource:           coreapi.TrackedResource{Resource: coreapi.NewResource(clusterID)},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterServiceID: ptr.To(metadataapi.Must(metadataapi.NewInternalID(testCSClusterIDStr)))},
	}
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(coreapihelpers.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName)))},
		Spec:           coreapi.ServiceProviderClusterSpec{ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{DesiredVersion: ptr.To(semver.MustParse("4.20.2"))}},
		Status:         coreapi.ServiceProviderClusterStatus{ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{ActiveVersions: []coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.1")), State: configv1.CompletedUpdate}}}},
	}
	return &triggerControlPlaneUpgradeSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)),
		clusterServiceClient:         client,
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}},
		activeOperationLister:        &corelistertesting.SliceActiveOperationLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{Desires: upgradeReadDesires(t, true)},
	}, cluster, spc
}

type failingUpgradeReadLister struct {
	kubeapplierlisters.ReadDesireLister
	err error
}

func (l *failingUpgradeReadLister) GetForCluster(context.Context, string, string, string, string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}

func TestTriggerControlPlaneUpgradeConditionDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*triggerControlPlaneUpgradeSyncer, *coreapi.ServiceProviderCluster, []*kubeapplierapi.ReadDesire)
		wantErr string
	}{
		{name: "eligible"},
		{name: "blocked", mutate: func(c *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, _ []*kubeapplierapi.ReadDesire) {
			c.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: upgradeReadDesires(t, false)}
		}, wantErr: `control plane upgrade from 4.19.1 to 4.20.2 blocked by ClusterVersionUpgradeable: reason="OperatorBlocked", message="original operator message"`},
		{name: "partial target uses completed source", mutate: func(c *triggerControlPlaneUpgradeSyncer, spc *coreapi.ServiceProviderCluster, _ []*kubeapplierapi.ReadDesire) {
			spc.Status.ControlPlaneVersion.ActiveVersions = append([]coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.1")), State: configv1.PartialUpdate}}, spc.Status.ControlPlaneVersion.ActiveVersions...)
			c.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: upgradeReadDesires(t, false)}
		}, wantErr: "blocked by ClusterVersionUpgradeable"},
		{name: "patch baseline skips minor condition", mutate: func(c *triggerControlPlaneUpgradeSyncer, spc *coreapi.ServiceProviderCluster, _ []*kubeapplierapi.ReadDesire) {
			spc.Status.ControlPlaneVersion.ActiveVersions[0].Version = ptr.To(semver.MustParse("4.20.1"))
			c.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: upgradeReadDesires(t, false)}
		}},
		{name: "no completed source", mutate: func(c *triggerControlPlaneUpgradeSyncer, spc *coreapi.ServiceProviderCluster, _ []*kubeapplierapi.ReadDesire) {
			spc.Status.ControlPlaneVersion.ActiveVersions[0].State = configv1.PartialUpdate
			c.readDesireLister = nil
		}, wantErr: "no completed control plane version"},
		{name: "missing mirror", mutate: func(c *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, _ []*kubeapplierapi.ReadDesire) {
			c.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{}
		}, wantErr: "missing HostedCluster observation"},
		{name: "read error", mutate: func(c *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, _ []*kubeapplierapi.ReadDesire) {
			c.readDesireLister = &failingUpgradeReadLister{err: errors.New("mirror unavailable")}
		}, wantErr: "mirror unavailable"},
		{name: "nil content", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, desires []*kubeapplierapi.ReadDesire) {
			desires[0].Status.KubeContent = nil
		}, wantErr: "missing HostedCluster observation"},
		{name: "empty content", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, desires []*kubeapplierapi.ReadDesire) {
			desires[0].Status.KubeContent.Raw = nil
		}, wantErr: "missing HostedCluster observation"},
		{name: "malformed content", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, desires []*kubeapplierapi.ReadDesire) {
			desires[0].Status.KubeContent.Raw = []byte("{")
		}, wantErr: "failed to unmarshal"},
		{name: "missing conditions", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, desires []*kubeapplierapi.ReadDesire) {
			desires[0].Status.KubeContent.Raw = []byte(`{"status":{}}`)
		}, wantErr: "no ReconciliationActive condition"},
		{name: "cached content after unsuccessful read still eligible", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.ServiceProviderCluster, desires []*kubeapplierapi.ReadDesire) {
			desires[0].Status.Conditions = []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionFalse}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			syncer, cluster, spc := newUpgradeTestSyncer(t, client)
			desires := upgradeReadDesires(t, true)
			syncer.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: desires}
			if tc.mutate != nil {
				tc.mutate(syncer, spc, desires)
			}
			before := spc.DeepCopy()
			csID := *cluster.ServiceProviderProperties.ClusterServiceID
			client.EXPECT().ListControlPlaneUpgradePolicies(csID, "creation_timestamp desc").Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator(nil, nil))
			if tc.wantErr == "" {
				builder := arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.20.2")
				client.EXPECT().PostControlPlaneUpgradePolicy(gomock.Any(), csID, builder).Return(metadataapi.Must(builder.Build()), nil).Times(1)
			}
			err := syncer.SyncOnce(t.Context(), upgradeTestKey())
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, before, spc, "eligibility must not write provider state or creation validations")
		})
	}
}

func TestTriggerControlPlaneUpgradeGuardsAvoidGateReads(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mutate         func(*triggerControlPlaneUpgradeSyncer, *coreapi.Cluster, *coreapi.ServiceProviderCluster)
		matchingPolicy bool
	}{
		{name: "deleting", mutate: func(_ *triggerControlPlaneUpgradeSyncer, c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			c.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
		}},
		{name: "no CS ID", mutate: func(_ *triggerControlPlaneUpgradeSyncer, c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			c.ServiceProviderProperties.ClusterServiceID = nil
		}},
		{name: "no provider state", mutate: func(c *triggerControlPlaneUpgradeSyncer, _ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			c.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
		}},
		{name: "no desired version", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Spec.ControlPlaneVersion.DesiredVersion = nil
		}},
		{name: "no active versions", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Status.ControlPlaneVersion.ActiveVersions = nil
		}},
		{name: "same active version", mutate: func(_ *triggerControlPlaneUpgradeSyncer, _ *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) {
			spc.Spec.ControlPlaneVersion.DesiredVersion = spc.Status.ControlPlaneVersion.ActiveVersions[0].Version
		}},
		{name: "initial create grace", mutate: func(c *triggerControlPlaneUpgradeSyncer, cluster *coreapi.Cluster, _ *coreapi.ServiceProviderCluster) {
			cluster.SystemData = &coreapi.SystemData{CreatedAt: ptr.To(c.clock.Now())}
			cluster.ServiceProviderProperties.ActiveOperationID = "create-operation"
			c.activeOperationLister = &corelistertesting.SliceActiveOperationLister{Operations: []*coreapi.Operation{{OperationID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/create-operation")), Request: cosmosstorageutils.OperationRequestCreate, Status: coreapi.ProvisioningStateAccepted}}}
		}},
		{name: "matching latest policy", matchingPolicy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			c, cluster, spc := newUpgradeTestSyncer(t, client)
			c.readDesireLister = nil // A premature mirror read panics instead of passing silently.
			if tc.mutate != nil {
				tc.mutate(c, cluster, spc)
			}
			if tc.matchingPolicy {
				policy := metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version(spc.Spec.ControlPlaneVersion.DesiredVersion.String()).Build())
				client.EXPECT().ListControlPlaneUpgradePolicies(*cluster.ServiceProviderProperties.ClusterServiceID, "creation_timestamp desc").Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{policy}, nil))
			}
			require.NoError(t, c.SyncOnce(t.Context(), upgradeTestKey()))
		})
	}
}

func TestTriggerControlPlaneUpgradeRetargeting(t *testing.T) {
	for _, reason := range []string{"UpdateInProgress", "ClusterVersionUpdating", "OperatorBlocked"} {
		t.Run(reason, func(t *testing.T) {
			client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			c, cluster, _ := newUpgradeTestSyncer(t, client)
			desires := upgradeReadDesires(t, false)
			var hc hsv1beta1.HostedCluster
			require.NoError(t, json.Unmarshal(desires[0].Status.KubeContent.Raw, &hc))
			hc.Status.Conditions[1].Reason = reason
			raw, err := json.Marshal(hc)
			require.NoError(t, err)
			desires[0].Status.KubeContent.Raw = raw
			c.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: desires}
			csID := *cluster.ServiceProviderProperties.ClusterServiceID
			latest := metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.20.1").State(arohcpv1alpha1.NewUpgradePolicyState().Value(arohcpv1alpha1.UpgradePolicyStateValueStarted)).Build())
			client.EXPECT().ListControlPlaneUpgradePolicies(csID, "creation_timestamp desc").Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{latest}, nil))
			if reason == "OperatorBlocked" {
				require.ErrorContains(t, c.SyncOnce(t.Context(), upgradeTestKey()), reason)
				return
			}
			builder := arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.20.2")
			client.EXPECT().PostControlPlaneUpgradePolicy(gomock.Any(), csID, builder).Return(metadataapi.Must(builder.Build()), nil)
			require.NoError(t, c.SyncOnce(t.Context(), upgradeTestKey()))
		})
	}
}
