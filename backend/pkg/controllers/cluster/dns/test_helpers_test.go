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
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var comparison = cmp.Options{cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}), cmp.Comparer(func(a, b metadataapi.InternalID) bool { return a.String() == b.String() }), cmpopts.EquateErrors()}

var testKey = controllerutils.HCPClusterKey{SubscriptionID: "sub", ResourceGroupName: "rg", HCPClusterName: "test"}
var testNow = time.Unix(1000000, 0)

func testContext(t *testing.T) context.Context {
	return utils.ContextWithLogger(t.Context(), logr.Discard())
}

func testCluster(domain, prefix string) *coreapi.Cluster {
	id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test"))
	pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	return &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub", CosmosETag: "cluster-etag", InstanceVersion: 3}, TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}}, CustomerProperties: coreapi.ClusterCustomerProperties{DNS: coreapi.CustomerDNSProfile{BaseDomainPrefix: prefix}}, ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterUID: "aro-uid", PendingClusterServiceID: &pending, DNS: coreapi.ServiceProviderDNSProfile{BaseDomain: domain}}}
}

func testProvider(domain string) *coreapi.ServiceProviderCluster {
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testCluster("", "").ID.String() + "/serviceProviderClusters/default")), PartitionKey: "sub", CosmosETag: "provider-etag", InstanceVersion: 2},
		Spec:           coreapi.ServiceProviderClusterSpec{DesiredDNSBaseDomain: domain, ManagementClusterResourceID: metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/stamp/managementClusters/default"))},
	}
}

func testClaim() *fleetapi.DNSClaim {
	id := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/dnsZones/example.com/dnsClaims/bcdf2"))
	return &fleetapi.DNSClaim{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "example.com"}, BaseDomain: "bcdf2.example.com", ClusterUUID: "aro-uid", ClusterResourceID: testCluster("", "").ID, CreatedAt: metav1.NewTime(testNow)}
}

// recordingCRUD captures the public storage boundary without implementing a
// database. Unexpected methods panic through the embedded interface.
type recordingCRUD[T any, P coreapi.CosmosMetadataAccessorPtr[T]] struct {
	cosmosstorageutils.ResourceCRUD[T, P]
	getObject    *T
	getErr       error
	gets         int
	writeErr     error
	creates      []*T
	replacements []*T
	deletes      []string
	deleteETags  []azcore.ETag
}

func (c *recordingCRUD[T, P]) Get(context.Context, string) (*T, error) {
	c.gets++
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.getObject == nil {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	return c.getObject, nil
}
func (c *recordingCRUD[T, P]) Create(_ context.Context, obj *T, _ *azcosmos.ItemOptions) (*T, error) {
	c.creates = append(c.creates, obj)
	return obj, c.writeErr
}
func (c *recordingCRUD[T, P]) Replace(_ context.Context, obj *T, _ *azcosmos.ItemOptions) (*T, error) {
	c.replacements = append(c.replacements, obj)
	return obj, c.writeErr
}
func (c *recordingCRUD[T, P]) DeleteWithETag(_ context.Context, name string, etag azcore.ETag) error {
	c.deletes = append(c.deletes, name)
	c.deleteETags = append(c.deleteETags, etag)
	return c.writeErr
}

type recordingClusterCRUD struct {
	clusterChildren
	*recordingCRUD[coreapi.Cluster, *coreapi.Cluster]
}

// DNS reconciliation must not touch child resources. Leaving this embedded
// interface nil makes accidental access fail the test.
type clusterChildren interface {
	corecosmosstorage.ControllerContainer
	corecosmosstorage.ManagementClusterContentContainer
	ExternalAuth(string) corecosmosstorage.ExternalAuthsCRUD
	NodePools(string) corecosmosstorage.NodePoolsCRUD
	SystemAdminCredentialRequests(string) corecosmosstorage.SystemAdminCredentialRequestsCRUD
	SystemAdminCredentialRevocations(string) corecosmosstorage.SystemAdminCredentialRevocationsCRUD
}

type recordingResources struct {
	corecosmosstorage.ResourcesDBClient
	clusters  recordingClusterCRUD
	providers recordingCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
}

func newRecordingResources() *recordingResources {
	return &recordingResources{clusters: recordingClusterCRUD{recordingCRUD: &recordingCRUD[coreapi.Cluster, *coreapi.Cluster]{}}}
}
func (c *recordingResources) HCPClusters(string, string) corecosmosstorage.HCPClusterCRUD {
	return &c.clusters
}
func (c *recordingResources) ServiceProviderClusters(string, string, string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &c.providers
}

type recordingFleet struct {
	fleetcosmosstorage.FleetDBClient
	claims recordingCRUD[fleetapi.DNSClaim, *fleetapi.DNSClaim]
}

func (c *recordingFleet) DNSClaims(string) cosmosstorageutils.ResourceCRUD[fleetapi.DNSClaim, *fleetapi.DNSClaim] {
	return &c.claims
}

type claimReadError struct {
	fleetlisters.DNSClaimLister
	err error
}

func (l claimReadError) Get(context.Context, string) (*fleetapi.DNSClaim, error) { return nil, l.err }

func claimLister(t *testing.T, claim *fleetapi.DNSClaim, err error) fleetlisters.DNSClaimLister {
	t.Helper()
	if err != nil {
		return claimReadError{err: err}
	}
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if claim != nil {
		require.NoError(t, indexer.Add(claim))
	}
	return fleetlisters.NewDNSClaimLister(indexer)
}

func compare[T any](t *testing.T, want, got T) {
	t.Helper()
	if diff := cmp.Diff(want, got, comparison...); len(diff) != 0 {
		t.Fatal(diff)
	}
}
