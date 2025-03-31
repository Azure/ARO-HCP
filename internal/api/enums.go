package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// DiskStorageAccountType represents supported Azure storage account types.
type DiskStorageAccountType string

const (
	DiskStorageAccountTypePremium_LRS     DiskStorageAccountType = "Premium_LRS"
	DiskStorageAccountTypeStandardSSD_LRS DiskStorageAccountType = "StandardSSD_LRS"
	DiskStorageAccountTypeStandard_LRS    DiskStorageAccountType = "Standard_LRS"
)

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

type Effect string

const (
	// EffectNoExecute - NoExecute taint effect
	EffectNoExecute Effect = "NoExecute"
	// EffectNoSchedule - NoSchedule taint effect
	EffectNoSchedule Effect = "NoSchedule"
	// EffectPreferNoSchedule - PreferNoSchedule taint effect
	EffectPreferNoSchedule Effect = "PreferNoSchedule"
)

// OptionalClusterCapability - Cluster capabilities that can be disabled.
type OptionalClusterCapability string

const (
	// OptionalClusterCapabilityImageRegistry - Enables the OpenShift internal image registry.
	OptionalClusterCapabilityImageRegistry OptionalClusterCapability = "ImageRegistry"
)
