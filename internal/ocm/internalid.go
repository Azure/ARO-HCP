package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

const (
	v1Pattern         = "/api/clusters_mgmt/v1"
	v1ClusterPattern  = v1Pattern + "/clusters/*"
	v1NodePoolPattern = v1ClusterPattern + "/node_pools/*"

	aroHcpV1Alpha1Pattern        = "/api/aro_hcp/v1alpha1"
	aroHcpV1Alpha1ClusterPattern = aroHcpV1Alpha1Pattern + "/clusters/*"
)

func GenerateClusterHREF(clusterName string) string {
	return v1Pattern + "/clusters/" + clusterName
}

func GenerateNodePoolHREF(clusterPath string, nodePoolName string) string {
	return clusterPath + "/node_pools/" + nodePoolName
}

// InternalID represents a Cluster Service resource.
type InternalID struct {
	path string
}

func (id *InternalID) validate() error {
	var match bool

	// This is where we will catch and convert any legacy API versions
	// to the version the RP is actively using.
	//
	// For example, once the RP is using "v2" we will convert "v1"
	// and any other legacy transitional versions we see to "v2".

	if match, _ = path.Match(v1ClusterPattern, id.path); match {
		return nil
	}

	if match, _ = path.Match(v1NodePoolPattern, id.path); match {
		return nil
	}

	if match, _ = path.Match(aroHcpV1Alpha1ClusterPattern, id.path); match {
		return nil
	}

	return fmt.Errorf("Invalid InternalID: %s", id.path)
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
	var match bool
	var kind string

	if match, _ = path.Match(v1ClusterPattern, id.path); match {
		kind = cmv1.ClusterKind
	}

	if match, _ = path.Match(v1NodePoolPattern, id.path); match {
		kind = cmv1.NodePoolKind
	}

	return kind
}

// GetClusterClient returns a v1 ClusterClient from the InternalID.
// This works for both cluster and node pool resources. The transport
// is most likely to be a Connection object from the SDK.
func (id *InternalID) GetClusterClient(transport http.RoundTripper) (*cmv1.ClusterClient, bool) {
	var thisPath string = id.path
	var lastPath string

	for thisPath != lastPath {
		if match, _ := path.Match(v1ClusterPattern, thisPath); match {
			return cmv1.NewClusterClient(transport, thisPath), true
		} else {
			lastPath = thisPath
			thisPath = path.Dir(thisPath)
		}
	}

	return nil, false
}

// GetNodePoolClient returns a v1 NodePoolClient from the InternalID.
// The transport is most likely to be a Connection object from the SDK.
func (id *InternalID) GetNodePoolClient(transport http.RoundTripper) (*cmv1.NodePoolClient, bool) {
	if match, _ := path.Match(v1NodePoolPattern, id.path); match {
		return cmv1.NewNodePoolClient(transport, id.path), true
	}
	return nil, false
}
