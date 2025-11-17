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
	"net/http"
	"path"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// Resource Keys
const (
	clusterKey      = "clusters"
	nodePoolKey     = "node_pools"
	externalAuthKey = "external_auth_config/external_auths"
)

var (
	v1Pattern        = "/api/clusters_mgmt/v1"
	v1ClusterPattern = path.Join(v1Pattern, clusterKey, "*")

	aroHcpV1Alpha1Pattern        = "/api/aro_hcp/v1alpha1"
	aroHcpV1Alpha1ClusterPattern = path.Join(aroHcpV1Alpha1Pattern, clusterKey, "*")
)

func GenerateClusterHREF(clusterName string) string {
	return path.Join(v1Pattern, clusterKey, clusterName)
}

func GenerateNodePoolHREF(clusterPath string, nodePoolName string) string {
	return path.Join(clusterPath, nodePoolKey, nodePoolName)
}

func GenerateExternalAuthHREF(clusterPath string, externalAuthName string) string {
	return path.Join(clusterPath, externalAuthKey, externalAuthName)
}

func GenerateBreakGlassCredentialHREF(clusterPath string, credentialName string) string {
	return path.Join(clusterPath, "break_glass_credentials", credentialName)
}

type InternalID = api.InternalID

// getClusterClient returns a v1 ClusterClient from the InternalID.
// This works for both cluster and node pool resources. The transport
// is most likely to be a Connection object from the SDK.
func getClusterClient(id InternalID, transport http.RoundTripper) (*cmv1.ClusterClient, bool) {
	switch matchClusterPath(id.Path()) {
	case v1ClusterPattern:
		return cmv1.NewClusterClient(transport, id.Path()), true

	case aroHcpV1Alpha1ClusterPattern:
		// support clusters received via ARO HCP APIs
		// without duplicating the whole codebase calling this method
		newPath := strings.ReplaceAll(id.Path(), aroHcpV1Alpha1Pattern, v1Pattern)
		return cmv1.NewClusterClient(transport, newPath), true

	default:
		return nil, false
	}
}

// getAroHCPClusterClient returns a arohcpv1alpha1 ClusterClient from the InternalID.
func getAroHCPClusterClient(id InternalID, transport http.RoundTripper) (*arohcpv1alpha1.ClusterClient, bool) {
	switch matchClusterPath(id.Path()) {
	case v1ClusterPattern:
		// support clusters received via cluster APIs
		// without duplicating the whole codebase calling this method
		newPath := strings.ReplaceAll(id.Path(), v1Pattern, aroHcpV1Alpha1Pattern)
		return arohcpv1alpha1.NewClusterClient(transport, newPath), true

	case aroHcpV1Alpha1ClusterPattern:
		return arohcpv1alpha1.NewClusterClient(transport, id.Path()), true

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
func GetNodePoolClient(id InternalID, transport http.RoundTripper) (*arohcpv1alpha1.NodePoolClient, bool) {
	if id.Kind() != arohcpv1alpha1.NodePoolKind {
		return nil, false
	}
	return arohcpv1alpha1.NewNodePoolClient(transport, id.Path()), true
}

// GetExternalAuthClient returns a arohcpv1alpha1 ExternalAuthClient from the InternalID.
// The transport is most likely to be a Connection object from the SDK.
func GetExternalAuthClient(id InternalID, transport http.RoundTripper) (*arohcpv1alpha1.ExternalAuthClient, bool) {
	if id.Kind() != arohcpv1alpha1.ExternalAuthKind {
		return nil, false
	}
	return arohcpv1alpha1.NewExternalAuthClient(transport, id.Path()), true
}

// GetBreakGlassCredentialClient returns a v1 BreakGlassCredentialClient
// from the InternalID. The transport is most likely to be a Connection
// object from the SDK.
func GetBreakGlassCredentialClient(id InternalID, transport http.RoundTripper) (*cmv1.BreakGlassCredentialClient, bool) {
	if id.Kind() != cmv1.BreakGlassCredentialKind {
		return nil, false
	}
	return cmv1.NewBreakGlassCredentialClient(transport, id.Path()), true
}
