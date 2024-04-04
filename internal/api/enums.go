package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import "fmt"

// NetworkType represents an OpenShift cluster network plugin.
type NetworkType int

const (
	NetworkTypeOpenShiftSDN NetworkType = iota
	NetworkTypeOVNKubernetes

	NetworkTypeOther // catch-all, must be last
)

func (v NetworkType) String() string {
	switch v {
	case NetworkTypeOpenShiftSDN:
		return "OpenShiftSDN"
	case NetworkTypeOVNKubernetes:
		return "OVNKubernetes"
	default:
		return "Other"
	}
}

func (v NetworkType) MarshalText() (text []byte, err error) {
	// NetworkTypeOther is a catch-all value.
	text = []byte(v.String())
	return
}

func (v *NetworkType) UnmarshalText(text []byte) error {
	for i := range NetworkTypeOther {
		if i.String() == string(text) {
			*v = i
			return nil
		}
	}

	// NetworkTypeOther is a catch-all value.
	*v = NetworkTypeOther
	return nil
}

// OutboundType represents a routing strategy to provide egress to the Internet.
type OutboundType int

const (
	OutboundTypeLoadBalancer OutboundType = iota

	outboundTypeLength // private, must be last
)

func (v OutboundType) String() string {
	switch v {
	case OutboundTypeLoadBalancer:
		return "loadBalancer"
	default:
		return ""
	}
}

func (v OutboundType) MarshalText() (text []byte, err error) {
	text = []byte(v.String())
	if len(text) == 0 {
		err = fmt.Errorf("Cannot marshal value %d", v)
	}
	return
}

func (v *OutboundType) UnmarshalText(text []byte) error {
	for i := range outboundTypeLength {
		if i.String() == string(text) {
			*v = i
			return nil
		}
	}

	return fmt.Errorf("Cannot unmarshal '%s' to a %T enum value", string(text), *v)
}

// Visibility represents the visibility of an API endpoint.
type Visibility int

const (
	VisibilityPublic Visibility = iota
	VisibilityPrivate

	visibilityLength // private, must be last
)

func (v Visibility) String() string {
	switch v {
	case VisibilityPublic:
		return "public"
	case VisibilityPrivate:
		return "private"
	default:
		return ""
	}
}

func (v Visibility) MarshalText() (text []byte, err error) {
	text = []byte(v.String())
	if len(text) == 0 {
		err = fmt.Errorf("Cannot marshal value %d", v)
	}
	return
}

func (v *Visibility) UnmarshalText(text []byte) error {
	for i := range visibilityLength {
		if i.String() == string(text) {
			*v = i
			return nil
		}
	}

	return fmt.Errorf("Cannot unmarshal '%s' to a %T enum value", string(text), *v)
}
