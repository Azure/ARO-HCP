// Copyright 2025 Microsoft Corporation
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

package ocm

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

const (
	v1Pattern                     = "/api/clusters_mgmt/v1"
	v1ClusterPattern              = v1Pattern + "/clusters/*"
	v1NodePoolPattern             = v1ClusterPattern + "/node_pools/*"
	v1BreakGlassCredentialPattern = v1ClusterPattern + "/break_glass_credentials/*"

	aroHcpV1Alpha1Pattern         = "/api/aro_hcp/v1alpha1"
	aroHcpV1Alpha1ClusterPattern  = aroHcpV1Alpha1Pattern + "/clusters/*"
	aroHcpV1Alpha1NodePoolPattern = aroHcpV1Alpha1ClusterPattern + "/node_pools/*"
)

func GenerateClusterHREF(clusterName string) string {
	return path.Join(v1Pattern, "clusters", clusterName)
}

func GenerateNodePoolHREF(clusterPath string, nodePoolName string) string {
	return path.Join(clusterPath, "node_pools", nodePoolName)
}

func GenerateBreakGlassCredentialHREF(clusterPath string, credentialName string) string {
	return path.Join(clusterPath, "break_glass_credentials", credentialName)
}

// InternalID represents a Cluster Service resource.
type InternalID struct {
	path string
	kind string
}

func (id *InternalID) validate() error {
	var match bool

	// This is where we will catch and convert any legacy API versions
	// to the version the RP is actively using.
	//
	// For example, once the RP is using "v2" we will convert "v1"
	// and any other legacy transitional versions we see to "v2".

	if match, _ = path.Match(v1ClusterPattern, id.path); match {
		id.kind = cmv1.ClusterKind
		return nil
	}

	if match, _ = path.Match(v1NodePoolPattern, id.path); match {
		id.kind = cmv1.NodePoolKind
		return nil
	}

	if match, _ = path.Match(v1BreakGlassCredentialPattern, id.path); match {
		id.kind = cmv1.BreakGlassCredentialKind
		return nil
	}

	if match, _ = path.Match(aroHcpV1Alpha1ClusterPattern, id.path); match {
		id.kind = arohcpv1alpha1.ClusterKind
		return nil
	}

	if match, _ = path.Match(aroHcpV1Alpha1NodePoolPattern, id.path); match {
		id.kind = arohcpv1alpha1.NodePoolKind
		return nil
	}

	return fmt.Errorf("invalid InternalID: %s", id.path)
}

// NewInternalID attempts to create a new InternalID from a Cluster Service
// API path, returning an error if the API path is invalid or unsupported.
func NewInternalID(path string) (InternalID, error) {
	internalID := InternalID{path: strings.ToLower(path)}
	if err := internalID.validate(); err != nil {
		return InternalID{}, err
	}
	return internalID, nil
}

// String allows an InternalID to be used as a fmt.Stringer.
func (id *InternalID) String() string {
	return id.path
}

// MarshalText allows an InternalID to be used as an encoding.TextMarshaler.
func (id InternalID) MarshalText() ([]byte, error) {
	return []byte(id.path), nil
}

// UnmarshalText allows an InternalID to be used as an encoding.TextUnmarshaler.
func (id *InternalID) UnmarshalText(text []byte) error {
	id.path = strings.ToLower(string(text))
	return id.validate()
}

// ID returns the last path element of the resource described by InternalID.
func (id *InternalID) ID() string {
	return path.Base(id.path)
}

// Kind returns the kind of resource described by InternalID, currently
// limited to "Cluster" and "NodePool".
func (id *InternalID) Kind() string {
	return id.kind
}

// GetClusterClient returns a v1 ClusterClient from the InternalID.
// This works for both cluster and node pool resources. The transport
// is most likely to be a Connection object from the SDK.
func (id *InternalID) GetClusterClient(transport http.RoundTripper) (*cmv1.ClusterClient, bool) {
	switch matchClusterPath(id.path) {
	case v1ClusterPattern:
		return cmv1.NewClusterClient(transport, id.path), true

	case aroHcpV1Alpha1ClusterPattern:
		// support clusters received via ARO HCP APIs
		// without duplicating the whole codebase calling this method
		newPath := strings.ReplaceAll(id.path, aroHcpV1Alpha1Pattern, v1Pattern)
		return cmv1.NewClusterClient(transport, newPath), true

	default:
		return nil, false
	}
}

// GetAroHCPClusterClient returns a arohcpv1alpha1 ClusterClient from the InternalID.
func (id *InternalID) GetAroHCPClusterClient(transport http.RoundTripper) (*arohcpv1alpha1.ClusterClient, bool) {
	switch matchClusterPath(id.path) {
	case v1ClusterPattern:
		// support clusters received via cluster APIs
		// without duplicating the whole codebase calling this method
		newPath := strings.ReplaceAll(id.path, v1Pattern, aroHcpV1Alpha1Pattern)
		return arohcpv1alpha1.NewClusterClient(transport, newPath), true

	case aroHcpV1Alpha1ClusterPattern:
		return arohcpv1alpha1.NewClusterClient(transport, id.path), true

	default:
		return nil, false
	}
}

func matchClusterPath(clusterPath string) string {
	var thisPath = clusterPath
	var lastPath string

	for thisPath != lastPath {
		if match, _ := path.Match(v1ClusterPattern, thisPath); match {
			return v1ClusterPattern
		} else if match, _ := path.Match(aroHcpV1Alpha1ClusterPattern, thisPath); match {
			return aroHcpV1Alpha1ClusterPattern
		} else {
			lastPath = thisPath
			thisPath = path.Dir(thisPath)
		}
	}

	return ""
}

// GetNodePoolClient returns a arohcpv1alpha1 NodePoolClient from the InternalID.
// The transport is most likely to be a Connection object from the SDK.
func (id *InternalID) GetNodePoolClient(transport http.RoundTripper) (*arohcpv1alpha1.NodePoolClient, bool) {
	if id.Kind() != arohcpv1alpha1.NodePoolKind {
		return nil, false
	}
	return arohcpv1alpha1.NewNodePoolClient(transport, id.path), true
}

// GetBreakGlassCredentialClient returns a v1 BreakGlassCredentialClient
// from the InternalID. The transport is most likely to be a Connection
// object from the SDK.
func (id *InternalID) GetBreakGlassCredentialClient(transport http.RoundTripper) (*cmv1.BreakGlassCredentialClient, bool) {
	if id.Kind() != cmv1.BreakGlassCredentialKind {
		return nil, false
	}
	return cmv1.NewBreakGlassCredentialClient(transport, id.path), true
}
