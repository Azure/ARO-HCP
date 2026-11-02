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

// NamespaceRole distinguishes the two namespaces a HostedCluster owns.
//
// The distinction has to be made from the name alone, because the whole point
// of a NamespaceDeletion is that neither the HostedCluster nor any customer
// Cosmos data is reliably there to ask.
type NamespaceRole string

const (
	// NamespaceRoleHostedCluster is the namespace holding the HostedCluster CR:
	// ocm-<envIdentifier>-<clusterID>.
	NamespaceRoleHostedCluster NamespaceRole = "HostedClusterNamespace"

	// NamespaceRoleControlPlane is the namespace holding the control plane
	// workloads: ocm-<envIdentifier>-<clusterID>-<hostedClusterName>.
	NamespaceRoleControlPlane NamespaceRole = "ControlPlaneNamespace"
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

// NamespaceRelationship is the result of classifying a requested namespace
// against its HostedCluster name.
type NamespaceRelationship struct {
	// Role is which of the two namespaces was requested.
	Role NamespaceRole

	// HostedClusterNamespace is the namespace holding the HostedCluster CR. The
	// controller reads the HostedCluster from here regardless of which role was
	// requested, because that is where it lives.
	HostedClusterNamespace string

	// ControlPlaneNamespace is the namespace holding the control plane.
	ControlPlaneNamespace string
}

// ClassifyNamespace determines whether namespaceName is the HostedCluster
// namespace or its control plane namespace, given the HostedCluster name.
//
// envIdentifier may be empty, in which case the environment segment is accepted
// as any non-empty label and only the structure is enforced. The admin API uses
// that form — it does not receive the backend's environment flag — while the
// backend anchors the check with the real value.
//
// Unsupported or ambiguous forms are rejected rather than guessed at. An
// arbitrary string-prefix match is deliberately not used.
func ClassifyNamespace(namespaceName, hostedClusterName, envIdentifier string) (*NamespaceRelationship, error) {
	if len(namespaceName) == 0 {
		return nil, fmt.Errorf("namespaceName is empty")
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

	// Rule 1: the name is itself a HostedCluster namespace.
	if _, ok := splitHostedClusterNamespace(namespaceName, envIdentifier); ok {
		return &NamespaceRelationship{
			Role:                   NamespaceRoleHostedCluster,
			HostedClusterNamespace: namespaceName,
			ControlPlaneNamespace:  namespaceName + "-" + suffix,
		}, nil
	}

	// Rule 2: the name is a control plane namespace, i.e. a HostedCluster
	// namespace plus the normalized HostedCluster name. Verified by
	// reconstruction rather than by parsing, so a HostedCluster name containing
	// a dash cannot confuse it.
	if base, ok := strings.CutSuffix(namespaceName, "-"+suffix); ok {
		if _, baseOK := splitHostedClusterNamespace(base, envIdentifier); baseOK {
			return &NamespaceRelationship{
				Role:                   NamespaceRoleControlPlane,
				HostedClusterNamespace: base,
				ControlPlaneNamespace:  namespaceName,
			}, nil
		}
	}

	// Rule 3: reject.
	return nil, fmt.Errorf(
		"namespaceName %q is not the HostedCluster namespace or its control plane namespace for hostedClusterName %q",
		namespaceName, hostedClusterName)
}

// splitHostedClusterNamespace reports whether name matches
// ocm-<env>-<32 lowercase alphanumerics>, returning the environment segment.
//
// The two classification rules are mutually exclusive because the cluster-ID
// segment has no dashes while a HostedCluster name is a short DNS label, so a
// control plane namespace can never satisfy this.
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
