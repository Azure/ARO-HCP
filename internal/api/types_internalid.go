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

package api

import (
	"fmt"
	"path"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

// Resource Keys
const (
	clusterKey               = "clusters"
	nodePoolKey              = "node_pools"
	externalAuthKey          = "external_auth_config/external_auths"
	breakGlassCredentialKey  = "break_glass_credentials"
	clusterProvisionShardKey = "provision_shard"
)

var (
	v1Pattern                     = "/api/clusters_mgmt/v1"
	v1ClusterPattern              = path.Join(v1Pattern, clusterKey, "*")
	v1NodePoolPattern             = path.Join(v1ClusterPattern, nodePoolKey, "*")
	v1ExternalAuthPattern         = path.Join(v1ClusterPattern, externalAuthKey, "*")
	v1BreakGlassCredentialPattern = path.Join(v1ClusterPattern, breakGlassCredentialKey, "*")

	aroHcpV1Alpha1Pattern                      = "/api/aro_hcp/v1alpha1"
	aroHcpV1Alpha1ClusterPattern               = path.Join(aroHcpV1Alpha1Pattern, clusterKey, "*")
	aroHcpV1Alpha1NodePoolPattern              = path.Join(aroHcpV1Alpha1ClusterPattern, nodePoolKey, "*")
	aroHcpV1Alpha1ExternalAuthPattern          = path.Join(aroHcpV1Alpha1ClusterPattern, externalAuthKey, "*")
	aroHcpV1Alpha1ClusterProvisionShardPattern = path.Join(aroHcpV1Alpha1ClusterPattern, clusterProvisionShardKey) + "$"
)

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

	if match, _ = path.Match(v1ExternalAuthPattern, id.path); match {
		id.kind = cmv1.ExternalAuthKind
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

	if match, _ = path.Match(aroHcpV1Alpha1ExternalAuthPattern, id.path); match {
		id.kind = arohcpv1alpha1.ExternalAuthKind
		return nil
	}

	if match, _ = path.Match(aroHcpV1Alpha1ClusterProvisionShardPattern, id.path); match {
		id.kind = arohcpv1alpha1.ProvisionShardKind
		return nil
	}

	return fmt.Errorf("invalid InternalID: %q", id.path)
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
	if len(text) == 0 {
		return nil
	}

	id.path = strings.ToLower(string(text))
	return id.validate()
}

// ID returns the last path element of the resource described by InternalID.
func (id *InternalID) ID() string {
	return path.Base(id.path)
}

func (id *InternalID) Path() string {
	return id.path
}

// ClusterID returns the path element following "clusters", if present.
func (id *InternalID) ClusterID() string {
	var returnNextElement bool

	for _, element := range strings.Split(id.path, "/") {
		if returnNextElement {
			return element
		} else if element == "clusters" {
			returnNextElement = true
		}
	}

	return ""
}

// Kind returns the kind of resource described by InternalID, currently
// limited to "Cluster" and "NodePool".
func (id *InternalID) Kind() string {
	return id.kind
}
