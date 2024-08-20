package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	cmv2alpha1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v2alpha1"
)

const (
	v2alpha1Pattern         = "/api/clusters_mgmt/v2alpha1"
	v2alpha1ClusterPattern  = v2alpha1Pattern + "/clusters/*"
	v2alpha1NodePoolPattern = v2alpha1ClusterPattern + "/node_pools/*"
)

// InternalID represents a Cluster Service resource.
type InternalID struct {
	path string
}

func (id *InternalID) validate() error {
	var match bool

	// This is where we will catch and convert any legacy API versions
	// to the version the RP is actively using.
	//
	// For example, once the RP is using "v2" we will convert "v2alpha1"
	// and any other legacy transitional versions we see to "v2".

	if match, _ = path.Match(v2alpha1ClusterPattern, id.path); match {
		return nil
	}

	if match, _ = path.Match(v2alpha1NodePoolPattern, id.path); match {
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

// Kind returns the kind of resource described by InternalID, currently
// limited to "Cluster" and "NodePool".
func (id *InternalID) Kind() string {
	var match bool
	var kind string

	if match, _ = path.Match(v2alpha1ClusterPattern, id.path); match {
		kind = cmv2alpha1.ClusterKind
	}

	if match, _ = path.Match(v2alpha1NodePoolPattern, id.path); match {
		kind = cmv2alpha1.NodePoolKind
	}

	return kind
}

// GetClusterClient returns a v1 ClusterClient from the InternalID.
// This works for both cluster and node pool resources. The transport
// is most likely to be a Connection object from the SDK.
func (id *InternalID) GetClusterClient(transport http.RoundTripper) (*cmv2alpha1.ClusterClient, bool) {
	var thisPath string = id.path
	var lastPath string

	for thisPath != lastPath {
		if match, _ := path.Match(v2alpha1ClusterPattern, thisPath); match {
			return cmv2alpha1.NewClusterClient(transport, thisPath), true
		} else {
			lastPath = thisPath
			thisPath = path.Dir(thisPath)
		}
	}

	return nil, false
}

// GetNodePoolClient returns a v1 NodePoolClient from the InternalID.
// The transport is most likely to be a Connection object from the SDK.
func (id *InternalID) GetNodePoolClient(transport http.RoundTripper) (*cmv2alpha1.NodePoolClient, bool) {
	if match, _ := path.Match(v2alpha1NodePoolPattern, id.path); match {
		return cmv2alpha1.NewNodePoolClient(transport, id.path), true
	}
	return nil, false
}
