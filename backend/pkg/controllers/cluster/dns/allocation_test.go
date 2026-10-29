// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type providerReadError struct {
	corelisters.ServiceProviderClusterLister
	err error
}

func (l providerReadError) Get(context.Context, string, string, string) (*coreapi.ServiceProviderCluster, error) {
	return nil, l.err
}

type subscriptionReadError struct {
	corelisters.SubscriptionLister
	err error
}

func (l subscriptionReadError) Get(context.Context, string) (*coreapi.Subscription, error) {
	return nil, l.err
}

type managementReadError struct {
	fleetlisters.ManagementClusterLister
	err error
}

func (l managementReadError) Get(context.Context, string) (*fleetapi.ManagementCluster, error) {
	return nil, l.err
}

func TestAllocationSyncOnce(t *testing.T) {
	const domain = "bcdf2.example.com"
	other := testClaim()
	other.ClusterUUID = "other"
	retiring := testClaim()
	stamp := metav1.NewTime(testNow)
	retiring.DeletionTimestamp = &stamp
	deleting := testCluster("", "")
	deleting.ServiceProviderProperties.DeletionTimestamp = &stamp
	created := testCluster("", "")
	created.ServiceProviderProperties.ClusterServiceID = created.ServiceProviderProperties.PendingClusterServiceID
	noPending := testCluster("", "")
	noPending.ServiceProviderProperties.PendingClusterServiceID = nil
	unplaced := testProvider("")
	unplaced.Spec.ManagementClusterResourceID = nil
	failure := errors.New("injected read failure")
	conflict := corecosmosstoragetesting.NewPreconditionFailedError()
	existingCS := metadataapi.Must(arohcpv1alpha1.NewCluster().Azure(arohcpv1alpha1.NewAzure().SubscriptionID("sub").ResourceGroupName("rg").ResourceName("test").TenantID("tenant").ManagedResourceGroupName("")).Build())
	tests := []struct {
		name                                                    string
		cluster                                                 *coreapi.Cluster
		provider                                                *coreapi.ServiceProviderCluster
		claim                                                   *fleetapi.DNSClaim
		claimErr, writeErr                                      error
		clusterErr, providerErr, subscriptionErr, managementErr error
		missingTenant, missingZone                              bool
		csErr                                                   error
		csClusters                                              []*arohcpv1alpha1.Cluster
		wantProvider                                            *coreapi.ServiceProviderCluster
		wantCluster                                             *coreapi.Cluster
		wantClaim                                               *fleetapi.DNSClaim
		wantErr                                                 string
		skipCS                                                  bool
	}{
		{name: "initial intent", cluster: testCluster("", ""), provider: testProvider(""), wantProvider: testProvider("<generated>")},
		{name: "custom prefix", cluster: testCluster("", "payments"), provider: testProvider(""), wantProvider: testProvider("<generated>")},
		{name: "exclusive claim", cluster: testCluster("", "cluster"), provider: testProvider(domain), wantClaim: testClaim()},
		{name: "commit and default legacy unallocated prefix", cluster: testCluster("", ""), provider: testProvider(domain), claim: testClaim(), wantCluster: testCluster(domain, "cluster")},
		{name: "commit custom prefix", cluster: testCluster("", "payments"), provider: testProvider(domain), claim: testClaim(), wantCluster: testCluster(domain, "payments")},
		{name: "collision", cluster: testCluster("", "cluster"), provider: testProvider(domain), claim: other, wantProvider: testProvider("<generated>")},
		{name: "retiring claim", cluster: testCluster("", "cluster"), provider: testProvider(domain), claim: retiring, wantProvider: testProvider("<generated>")},
		{name: "committed skips dependency reads", cluster: testCluster(domain, "cluster"), providerErr: failure, skipCS: true},
		{name: "committed with different intent", cluster: testCluster("other.example.com", "cluster"), provider: testProvider(domain), skipCS: true},
		{name: "committed with conflicting claim", cluster: testCluster(domain, "cluster"), provider: testProvider(domain), claim: other, skipCS: true},
		{name: "claim read failure", cluster: testCluster("", "cluster"), provider: testProvider(domain), claimErr: failure, wantErr: failure.Error()},
		{name: "provider replacement precondition failed", cluster: testCluster("", "cluster"), provider: testProvider(""), writeErr: conflict, wantProvider: testProvider("<generated>")},
		{name: "claim already created", cluster: testCluster("", "cluster"), provider: testProvider(domain), writeErr: &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"}, wantClaim: testClaim()},
		{name: "commit precondition failed", cluster: testCluster("", "cluster"), provider: testProvider(domain), claim: testClaim(), writeErr: conflict, wantCluster: testCluster(domain, "cluster")},
		{name: "claim create precondition failed", cluster: testCluster("", "cluster"), provider: testProvider(domain), writeErr: conflict, wantClaim: testClaim()},
		{name: "provider replacement fails", cluster: testCluster("", "cluster"), provider: testProvider(""), writeErr: failure, wantProvider: testProvider("<generated>"), wantErr: failure.Error()},
		{name: "claim create fails", cluster: testCluster("", "cluster"), provider: testProvider(domain), writeErr: failure, wantClaim: testClaim(), wantErr: failure.Error()},
		{name: "commit fails", cluster: testCluster("", "cluster"), provider: testProvider(domain), claim: testClaim(), writeErr: failure, wantCluster: testCluster(domain, "cluster"), wantErr: failure.Error()},
		{name: "CS lookup fails", cluster: testCluster("", "cluster"), provider: testProvider(domain), csErr: failure, wantErr: failure.Error()},
		{name: "missing cluster", skipCS: true},
		{name: "deleting", cluster: deleting, skipCS: true},
		{name: "created", cluster: created, skipCS: true},
		{name: "no pending CS ID", cluster: noPending, skipCS: true},
		{name: "unplaced", cluster: testCluster("", ""), provider: unplaced, skipCS: true},
		{name: "missing provider", cluster: testCluster("", ""), skipCS: true, wantErr: "404"},
		{name: "adopt existing CS before naming", cluster: testCluster("", ""), provider: testProvider(""), csClusters: []*arohcpv1alpha1.Cluster{existingCS}},
		{name: "cluster read failure", clusterErr: failure, skipCS: true, wantErr: failure.Error()},
		{name: "provider read failure", cluster: testCluster("", ""), providerErr: failure, skipCS: true, wantErr: failure.Error()},
		{name: "subscription read failure", cluster: testCluster("", ""), provider: testProvider(""), subscriptionErr: failure, skipCS: true, wantErr: failure.Error()},
		{name: "missing tenant", cluster: testCluster("", ""), provider: testProvider(""), missingTenant: true, skipCS: true, wantErr: "no tenant ID"},
		{name: "management read failure", cluster: testCluster("", ""), provider: testProvider(""), managementErr: failure, wantErr: failure.Error()},
		{name: "missing parent zone", cluster: testCluster("", ""), provider: testProvider(""), missingZone: true, wantErr: "no public DNS zone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := newRecordingResources()
			resources.clusters.writeErr = tt.writeErr
			resources.providers.writeErr = tt.writeErr
			fleet := &recordingFleet{}
			fleet.claims.writeErr = tt.writeErr
			client := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			if !tt.skipCS {
				client.EXPECT().ListClusters(gomock.Any()).Return(ocm.NewSimpleClusterListIterator(tt.csClusters, tt.csErr))
			}
			clusters := &corelistertesting.SliceClusterLister{}
			if tt.cluster != nil {
				clusters.Clusters = []*coreapi.Cluster{tt.cluster}
			}
			providers := &corelistertesting.SliceServiceProviderClusterLister{}
			if tt.provider != nil {
				providers.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{tt.provider}
			}
			subscription := &coreapi.Subscription{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub"))}, Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To("tenant")}}
			management := &fleetapi.ManagementCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testProvider("").Spec.ManagementClusterResourceID}, Status: fleetapi.ManagementClusterStatus{PublicDNSZoneResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com"))}}
			if tt.missingTenant {
				subscription.Properties.TenantId = nil
			}
			if tt.missingZone {
				management.Status.PublicDNSZoneResourceID = nil
			}
			controller := &dnsAllocationController{resourcesDBClient: resources, fleetDBClient: fleet, clusterLister: clusters, serviceProviderClusterLister: providers, subscriptionsLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}}, managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{management}}, dnsClaimLister: claimLister(t, tt.claim, tt.claimErr), clustersServiceClient: client, clock: clocktesting.NewFakePassiveClock(testNow)}
			if tt.clusterErr != nil {
				controller.clusterLister = clusterReadError{err: tt.clusterErr}
			}
			if tt.providerErr != nil {
				controller.serviceProviderClusterLister = providerReadError{err: tt.providerErr}
			}
			if tt.subscriptionErr != nil {
				controller.subscriptionsLister = subscriptionReadError{err: tt.subscriptionErr}
			}
			if tt.managementErr != nil {
				controller.managementClusterLister = managementReadError{err: tt.managementErr}
			}
			beforeCluster, beforeProvider, beforeClaim := tt.cluster.DeepCopy(), tt.provider.DeepCopy(), tt.claim.DeepCopy()
			err := controller.SyncOnce(testContext(t), testKey)
			if len(tt.wantErr) > 0 {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			var wantProviders []*coreapi.ServiceProviderCluster
			if tt.wantProvider != nil {
				require.Len(t, resources.providers.replacements, 1)
				candidate := resources.providers.replacements[0].Spec.DesiredDNSBaseDomain
				require.Regexp(t, `^[bcdfghjklmnpqrstvwxz2456789]{5}\.example\.com$`, candidate)
				want := tt.wantProvider.DeepCopy()
				want.Spec.DesiredDNSBaseDomain = candidate
				wantProviders = []*coreapi.ServiceProviderCluster{want}
			}
			compare(t, wantProviders, resources.providers.replacements)
			var wantClusters []*coreapi.Cluster
			if tt.wantCluster != nil {
				wantClusters = []*coreapi.Cluster{tt.wantCluster}
			}
			compare(t, wantClusters, resources.clusters.replacements)
			var wantClaims []*fleetapi.DNSClaim
			if tt.wantClaim != nil {
				wantClaims = []*fleetapi.DNSClaim{tt.wantClaim}
			}
			compare(t, wantClaims, fleet.claims.creates)
			require.Empty(t, fleet.claims.replacements)
			require.Empty(t, fleet.claims.deletes)
			compare(t, beforeCluster, tt.cluster)
			compare(t, beforeProvider, tt.provider)
			compare(t, beforeClaim, tt.claim)
		})
	}
}

func TestAllocateReturnsWriteErrors(t *testing.T) {
	const domain = "bcdf2.example.com"
	for _, writeErr := range []error{
		corecosmosstoragetesting.NewPreconditionFailedError(),
		&azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"},
	} {
		for _, tc := range []struct {
			name          string
			desiredDomain string
			claim         *fleetapi.DNSClaim
		}{
			{name: "persist intent"},
			{name: "create claim", desiredDomain: domain},
			{name: "commit domain", desiredDomain: domain, claim: testClaim()},
		} {
			t.Run(tc.name+"/"+writeErr.Error(), func(t *testing.T) {
				resources := newRecordingResources()
				resources.providers.writeErr = writeErr
				resources.clusters.writeErr = writeErr
				fleet := &recordingFleet{}
				fleet.claims.writeErr = writeErr
				controller := &dnsAllocationController{
					resourcesDBClient: resources,
					fleetDBClient:     fleet,
					dnsClaimLister:    claimLister(t, tc.claim, nil),
					clock:             clocktesting.NewFakePassiveClock(testNow),
				}
				err := controller.allocate(testContext(t), testCluster("", "cluster"), testProvider(tc.desiredDomain), "example.com")
				require.ErrorIs(t, err, writeErr, "allocate must preserve write errors for its caller")
			})
		}
	}
}
