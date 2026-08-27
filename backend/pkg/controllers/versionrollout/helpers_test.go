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

package versionrollout

import (
	"context"
	"strings"

	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
)

// v returns a pointer to the parsed exact version, panicking on bad input.
func v(s string) *semver.Version {
	parsed := semver.MustParse(s)
	return &parsed
}

// completed / partial build active-version entries.
func completed(version string) coreapi.HCPClusterActiveVersion {
	return coreapi.HCPClusterActiveVersion{Version: v(version), State: configv1.CompletedUpdate}
}

func partial(version string) coreapi.HCPClusterActiveVersion {
	return coreapi.HCPClusterActiveVersion{Version: v(version), State: configv1.PartialUpdate}
}

func newTestCluster(name, channelGroup, versionID string) *coreapi.HCPOpenShiftCluster {
	id := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name,
	))
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: strings.ToLower(id.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: id, Name: name, Type: id.ResourceType.String()},
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Version: coreapi.VersionProfile{ID: versionID, ChannelGroup: channelGroup},
		},
	}
}

func newTestSPC(clusterName string, desired *semver.Version, active []coreapi.HCPClusterActiveVersion, pinned *coreapi.ServiceProviderClusterPinnedVersion) *coreapi.ServiceProviderCluster {
	var pinnedValue coreapi.ServiceProviderClusterPinnedVersion
	if pinned != nil {
		pinnedValue = *pinned
	}
	id := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: strings.ToLower(id.SubscriptionID),
		},
		Spec: coreapi.ServiceProviderClusterSpec{
			ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{DesiredVersion: desired},
			PinnedVersion:       pinnedValue,
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{ActiveVersions: active},
		},
	}
}

func newTestRollout(channel string, best *semver.Version, status fleetapi.ControlPlaneVersionRolloutStatus) *fleetapi.ControlPlaneVersionRollout {
	id := metadataapi.Must(fleetapi.ToControlPlaneVersionRolloutResourceID(channel))
	return &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: strings.ToLower(id.Name),
		},
		Spec:   fleetapi.ControlPlaneVersionRolloutSpec{BestExactVersion: best},
		Status: status,
	}
}

// fakeRolloutStore is an in-memory RolloutLister + RolloutWriter for tests.
type fakeRolloutStore struct {
	rollouts   map[string]*fleetapi.ControlPlaneVersionRollout
	replaceErr error // when set, Replace returns this (e.g. a precondition failure)
	replaces   int
}

func newFakeRolloutStore(rollouts ...*fleetapi.ControlPlaneVersionRollout) *fakeRolloutStore {
	m := make(map[string]*fleetapi.ControlPlaneVersionRollout, len(rollouts))
	for _, r := range rollouts {
		m[r.GetStampIdentifier()] = r
	}
	return &fakeRolloutStore{rollouts: m}
}

func (f *fakeRolloutStore) Get(_ context.Context, ystreamChannel string) (*fleetapi.ControlPlaneVersionRollout, error) {
	r, ok := f.rollouts[ystreamChannel]
	if !ok {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	return r.DeepCopy(), nil
}

func (f *fakeRolloutStore) List(_ context.Context) ([]*fleetapi.ControlPlaneVersionRollout, error) {
	out := make([]*fleetapi.ControlPlaneVersionRollout, 0, len(f.rollouts))
	for _, r := range f.rollouts {
		out = append(out, r.DeepCopy())
	}
	return out, nil
}

func (f *fakeRolloutStore) Replace(_ context.Context, newRollout, _ *fleetapi.ControlPlaneVersionRollout) (*fleetapi.ControlPlaneVersionRollout, error) {
	if f.replaceErr != nil {
		return nil, f.replaceErr
	}
	f.replaces++
	f.rollouts[newRollout.GetStampIdentifier()] = newRollout.DeepCopy()
	return newRollout, nil
}

// serviceProviderClusterName returns the backing cluster's name (its resource
// ID's parent), for test assertions.
func serviceProviderClusterName(serviceProviderCluster *coreapi.ServiceProviderCluster) string {
	if serviceProviderCluster.ResourceID == nil || serviceProviderCluster.ResourceID.Parent == nil {
		return ""
	}
	return serviceProviderCluster.ResourceID.Parent.Name
}

// firstNSelector selects the first n candidates deterministically.
type firstNSelector struct{}

func (firstNSelector) Select(candidates []*coreapi.ServiceProviderCluster, n int) []*coreapi.ServiceProviderCluster {
	if n <= 0 {
		return nil
	}
	if n > len(candidates) {
		n = len(candidates)
	}
	return candidates[:n]
}

// fakeBestVersionSelector returns a fixed graph best version.
type fakeBestVersionSelector struct {
	best *semver.Version
	err  error
}

func (f fakeBestVersionSelector) BestExactVersionForChannel(_ context.Context, _ string) (*semver.Version, error) {
	return f.best, f.err
}
