package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// NetworkType represents an OpenShift cluster network plugin.
type NetworkType string

const (
	NetworkTypeOVNKubernetes NetworkType = "OVNKubernetes"
	NetworkTypeOther         NetworkType = "Other"
)

// OutboundType represents a routing strategy to provide egress to the Internet.
type OutboundType string

const (
	OutboundTypeLoadBalancer OutboundType = "loadBalancer"
)

// Visibility represents the visibility of an API endpoint.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)
