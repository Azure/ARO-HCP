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

package fleetapihelpers

import (
	"fmt"
	"regexp"
	"strings"
)

// clusterIDPattern matches the 32-character segment that Cluster Service
// historically placed in these namespace names.
//
// This is NOT a dependency on Cluster Service. The segment is treated as an
// opaque string that is already present in a name on the management cluster: it
// is never resolved against Cluster Service, never sent to it, never used to
// look anything up, and never used as an ownership match. No code path here
// asks Cluster Service anything, so none breaks when it is retired. What the
// pattern asserts is purely a statement about string shape on namespaces that
// already exist, which is the only kind of namespace this API ever acts on.
var clusterIDPattern = regexp.MustCompile(`^[a-z0-9]{32}$`)

// NamespaceRelationship identifies the HostedCluster namespace and its derived
// control plane namespace.
type NamespaceRelationship struct {
	// HostedClusterNamespace is the namespace holding the HostedCluster CR. The
	// controller reads the HostedCluster from here.
	HostedClusterNamespace string

	// ControlPlaneNamespace is the namespace holding the control plane.
	ControlPlaneNamespace string
}

// RelateHostedClusterNamespace validates the requested HostedCluster namespace
// and derives its control plane namespace from the HostedCluster name.
//
// envIdentifier may be empty, in which case the environment segment is accepted
// as any non-empty label and only the structure is enforced. The admin API uses
// that form — it does not receive the backend's environment flag — while the
// backend anchors the check with the real value.
//
// Unsupported or ambiguous forms are rejected rather than guessed at. An
// arbitrary string-prefix match is deliberately not used.
func RelateHostedClusterNamespace(hostedClusterNamespaceName, hostedClusterName, envIdentifier string) (*NamespaceRelationship, error) {
	if len(hostedClusterNamespaceName) == 0 {
		return nil, fmt.Errorf("hostedClusterNamespaceName is empty")
	}
	if len(hostedClusterName) == 0 {
		return nil, fmt.Errorf("hostedClusterName is empty")
	}

	// HyperShift builds the control plane namespace as
	// <hostedClusterNamespace>-<ReplaceAll(hostedClusterName, ".", "-")>. The
	// existing backend helper does no dot normalization because a domain prefix
	// is a DNS label, but the parser must still apply HyperShift's rule rather
	// than assume the simpler form.
	suffix := strings.ReplaceAll(hostedClusterName, ".", "-")
	// Reject a derived control plane namespace before testing the general
	// grammar. A 32-character HostedCluster name could otherwise look like the
	// cluster-ID segment of a HostedCluster namespace.
	if base, ok := strings.CutSuffix(hostedClusterNamespaceName, "-"+suffix); ok {
		if _, baseOK := splitHostedClusterNamespace(base, envIdentifier); baseOK {
			return nil, fmt.Errorf("hostedClusterNamespaceName %q is a control plane namespace", hostedClusterNamespaceName)
		}
	}

	if _, ok := splitHostedClusterNamespace(hostedClusterNamespaceName, envIdentifier); ok {
		return &NamespaceRelationship{
			HostedClusterNamespace: hostedClusterNamespaceName,
			ControlPlaneNamespace:  hostedClusterNamespaceName + "-" + suffix,
		}, nil
	}

	return nil, fmt.Errorf(
		"hostedClusterNamespaceName %q is not a HostedCluster namespace",
		hostedClusterNamespaceName)
}

// splitHostedClusterNamespace reports whether name matches
// ocm-<env>-<32 lowercase alphanumerics>, returning the environment segment.
//
// The derived control plane namespace is rejected before this parser runs.
func splitHostedClusterNamespace(name, envIdentifier string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "ocm-")
	if !ok {
		return "", false
	}

	// The cluster ID is the final dash-separated segment; everything between
	// "ocm-" and it is the environment identifier, which may itself contain
	// dashes.
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 {
		return "", false
	}
	env, clusterID := rest[:idx], rest[idx+1:]
	if !clusterIDPattern.MatchString(clusterID) {
		return "", false
	}
	if len(env) == 0 {
		return "", false
	}
	if len(envIdentifier) > 0 && env != envIdentifier {
		return "", false
	}
	return env, true
}
