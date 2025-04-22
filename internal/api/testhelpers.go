package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"io"
	"log/slog"
	"path"
	"testing"

	"dario.cat/mergo"
	validator "github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// The definitions in this file are meant for unit tests.

const (
	TestAPIVersion        = "2024-06-10-preview"
	TestTenantID          = "00000000-0000-0000-0000-000000000000"
	TestSubscriptionID    = "11111111-1111-1111-1111-111111111111"
	TestResourceGroupName = "testResourceGroup"
	TestClusterName       = "testCluster"
	TestNodePoolName      = "testNodePool"
)

var (
	TestGroupResourceID    = path.Join("/subscriptions", TestSubscriptionID, "resourceGroups", TestResourceGroupName)
	TestClusterResourceID  = path.Join(TestGroupResourceID, "providers", ProviderNamespace, ClusterResourceTypeName, TestClusterName)
	TestNodePoolResourceID = path.Join(TestClusterResourceID, NodePoolResourceTypeName, TestNodePoolName)
)

func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func NewTestValidator() *validator.Validate {
	validate := NewValidator()

	validate.RegisterAlias("enum_diskstorageaccounttype", EnumValidateTag(
		DiskStorageAccountTypePremium_LRS,
		DiskStorageAccountTypeStandardSSD_LRS,
		DiskStorageAccountTypeStandard_LRS))
	validate.RegisterAlias("enum_effect", EnumValidateTag(
		EffectNoExecute,
		EffectNoSchedule,
		EffectPreferNoSchedule))
	validate.RegisterAlias("enum_networktype", EnumValidateTag(
		NetworkTypeOVNKubernetes,
		NetworkTypeOther))
	validate.RegisterAlias("enum_outboundtype", EnumValidateTag(
		OutboundTypeLoadBalancer))
	validate.RegisterAlias("enum_visibility", EnumValidateTag(
		VisibilityPublic,
		VisibilityPrivate))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(
		arm.ManagedServiceIdentityTypeNone,
		arm.ManagedServiceIdentityTypeSystemAssigned,
		arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		arm.ManagedServiceIdentityTypeUserAssigned))
	validate.RegisterAlias("enum_optionalclustercapability", EnumValidateTag(
		OptionalClusterCapabilityImageRegistry))

	return validate
}

func NewTestUserAssignedIdentity(name string) string {
	return path.Join(TestGroupResourceID, "providers", "Microsoft.ManagedIdentity", "userAssignedIdentities", name)
}

func MinimumValidClusterTestCase() *HCPOpenShiftCluster {
	resource := NewDefaultHCPOpenShiftCluster()
	resource.Properties.Platform.SubnetID = path.Join(TestGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", "testVirtualNetwork", "subnets")
	resource.Properties.Platform.NetworkSecurityGroupID = path.Join(TestGroupResourceID, "providers", "Microsoft.Network", "networkSecurityGroups", "testNetworkSecurityGroup")
	return resource
}

func ClusterTestCase(t *testing.T, tweaks *HCPOpenShiftCluster) *HCPOpenShiftCluster {
	resource := MinimumValidClusterTestCase()
	require.NoError(t, mergo.Merge(resource, tweaks, mergo.WithOverride))
	return resource
}

func MinimumValidNodePoolTestCase() *HCPOpenShiftClusterNodePool {
	resource := NewDefaultHCPOpenShiftClusterNodePool()
	resource.Properties.Platform.VMSize = "Standard_D8s_v3"
	return resource
}

func NodePoolTestCase(t *testing.T, tweaks *HCPOpenShiftClusterNodePool) *HCPOpenShiftClusterNodePool {
	nodePool := MinimumValidNodePoolTestCase()
	require.NoError(t, mergo.Merge(nodePool, tweaks, mergo.WithOverride))
	return nodePool
}
