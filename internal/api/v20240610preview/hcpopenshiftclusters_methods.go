package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type HcpOpenShiftClusterResource struct {
	generated.HcpOpenShiftClusterResource
}

func newVersionProfile(from *api.VersionProfile) *generated.VersionProfile {
	return &generated.VersionProfile{
		ID:                api.Ptr(from.ID),
		ChannelGroup:      api.Ptr(from.ChannelGroup),
		AvailableUpgrades: api.StringSliceToStringPtrSlice(from.AvailableUpgrades),
	}
}

func newDNSProfile(from *api.DNSProfile) *generated.DNSProfile {
	return &generated.DNSProfile{
		BaseDomain:       api.Ptr(from.BaseDomain),
		BaseDomainPrefix: api.Ptr(from.BaseDomainPrefix),
	}
}

func newNetworkProfile(from *api.NetworkProfile) *generated.NetworkProfile {
	return &generated.NetworkProfile{
		NetworkType: api.Ptr(generated.NetworkType(from.NetworkType)),
		PodCidr:     api.Ptr(from.PodCIDR),
		ServiceCidr: api.Ptr(from.ServiceCIDR),
		MachineCidr: api.Ptr(from.MachineCIDR),
		HostPrefix:  api.Ptr(from.HostPrefix),
	}
}

func newConsoleProfile(from *api.ConsoleProfile) *generated.ConsoleProfile {
	return &generated.ConsoleProfile{
		URL: api.Ptr(from.URL),
	}
}

func newAPIProfile(from *api.APIProfile) *generated.APIProfile {
	return &generated.APIProfile{
		URL:        api.Ptr(from.URL),
		Visibility: api.Ptr(generated.Visibility(from.Visibility)),
	}
}

func newPlatformProfile(from *api.PlatformProfile) *generated.PlatformProfile {
	return &generated.PlatformProfile{
		ManagedResourceGroup:    api.Ptr(from.ManagedResourceGroup),
		SubnetID:                api.Ptr(from.SubnetID),
		OutboundType:            api.Ptr(generated.OutboundType(from.OutboundType)),
		NetworkSecurityGroupID:  api.Ptr(from.NetworkSecurityGroupID),
		OperatorsAuthentication: newOperatorsAuthenticationProfile(&from.OperatorsAuthentication),
	}
}

func newOperatorsAuthenticationProfile(from *api.OperatorsAuthenticationProfile) *generated.OperatorsAuthenticationProfile {
	return &generated.OperatorsAuthenticationProfile{
		UserAssignedIdentities: newUserAssignedIdentitiesProfile(&from.UserAssignedIdentities),
	}
}

func newUserAssignedIdentitiesProfile(from *api.UserAssignedIdentitiesProfile) *generated.UserAssignedIdentitiesProfile {
	return &generated.UserAssignedIdentitiesProfile{
		ControlPlaneOperators:  api.StringMapToStringPtrMap(from.ControlPlaneOperators),
		DataPlaneOperators:     api.StringMapToStringPtrMap(from.DataPlaneOperators),
		ServiceManagedIdentity: api.Ptr(from.ServiceManagedIdentity),
	}
}

func (v version) NewHCPOpenShiftCluster(from *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	if from == nil {
		from = api.NewDefaultHCPOpenShiftCluster()
	}

	out := &HcpOpenShiftClusterResource{
		generated.HcpOpenShiftClusterResource{
			ID:       api.Ptr(from.Resource.ID),
			Name:     api.Ptr(from.Resource.Name),
			Type:     api.Ptr(from.Resource.Type),
			Location: api.Ptr(from.TrackedResource.Location),
			Tags:     api.StringMapToStringPtrMap(from.TrackedResource.Tags),
			Identity: &generated.ManagedServiceIdentity{
				Type:        api.Ptr(generated.ManagedServiceIdentityType(from.Identity.Type)),
				PrincipalID: api.Ptr(from.Identity.PrincipalID),
				TenantID:    api.Ptr(from.Identity.TenantID),
				//as UserAssignedIdentities is of a different type so using convertUserAssignedIdentities instead of StringMapToStringPtrMap
				UserAssignedIdentities: convertUserAssignedIdentities(from.Identity.UserAssignedIdentities),
			},
			Properties: &generated.HcpOpenShiftClusterProperties{
				ProvisioningState:             api.Ptr(generated.ProvisioningState(from.Properties.ProvisioningState)),
				Version:                       newVersionProfile(&from.Properties.Version),
				DNS:                           newDNSProfile(&from.Properties.DNS),
				Network:                       newNetworkProfile(&from.Properties.Network),
				Console:                       newConsoleProfile(&from.Properties.Console),
				API:                           newAPIProfile(&from.Properties.API),
				DisableUserWorkloadMonitoring: api.Ptr(from.Properties.DisableUserWorkloadMonitoring),
				Platform:                      newPlatformProfile(&from.Properties.Platform),
				IssuerURL:                     api.Ptr(from.Properties.IssuerURL),
			},
		},
	}

	if from.Resource.SystemData != nil {
		out.SystemData = &generated.SystemData{
			CreatedBy:          api.Ptr(from.Resource.SystemData.CreatedBy),
			CreatedByType:      api.Ptr(generated.CreatedByType(from.Resource.SystemData.CreatedByType)),
			CreatedAt:          from.Resource.SystemData.CreatedAt,
			LastModifiedBy:     api.Ptr(from.Resource.SystemData.LastModifiedBy),
			LastModifiedByType: api.Ptr(generated.CreatedByType(from.Resource.SystemData.LastModifiedByType)),
			LastModifiedAt:     from.Resource.SystemData.LastModifiedAt,
		}
	}

	return out
}

func (c *HcpOpenShiftClusterResource) Normalize(out *api.HCPOpenShiftCluster) {
	if c.ID != nil {
		out.Resource.ID = *c.ID
	}
	if c.Name != nil {
		out.Resource.Name = *c.Name
	}
	if c.Type != nil {
		out.Resource.Type = *c.Type
	}
	if c.SystemData != nil {
		out.Resource.SystemData = &arm.SystemData{
			CreatedAt:      c.SystemData.CreatedAt,
			LastModifiedAt: c.SystemData.LastModifiedAt,
		}
		if c.SystemData.CreatedBy != nil {
			out.Resource.SystemData.CreatedBy = *c.SystemData.CreatedBy
		}
		if c.SystemData.CreatedByType != nil {
			out.Resource.SystemData.CreatedByType = arm.CreatedByType(*c.SystemData.CreatedByType)
		}
		if c.SystemData.LastModifiedBy != nil {
			out.Resource.SystemData.LastModifiedBy = *c.SystemData.LastModifiedBy
		}
		if c.SystemData.LastModifiedByType != nil {
			out.Resource.SystemData.LastModifiedByType = arm.CreatedByType(*c.SystemData.LastModifiedByType)
		}
	}
	if c.Location != nil {
		out.TrackedResource.Location = *c.Location
	}
	if c.Identity != nil {
		if c.Identity.PrincipalID != nil {
			out.Identity.PrincipalID = *c.Identity.PrincipalID
		}
		if c.Identity.TenantID != nil {
			out.Identity.TenantID = *c.Identity.TenantID
		}
		if c.Identity.Type != nil {
			out.Identity.Type = (arm.ManagedServiceIdentityType)(*c.Identity.Type)
		}
		if c.Identity.UserAssignedIdentities != nil {
			normalizeIdentityUserAssignedIdentities(c.Identity.UserAssignedIdentities, &out.Identity.UserAssignedIdentities)
		}
	}
	// Per RPC-Patch-V1-04, the Tags field does NOT follow
	// JSON merge-patch (RFC 7396) semantics:
	//
	//   When Tags are patched, the tags from the request
	//   replace all existing tags for the resource
	//
	out.Tags = api.StringPtrMapToStringMap(c.Tags)
	if c.Properties != nil {
		if c.Properties.ProvisioningState != nil {
			out.Properties.ProvisioningState = arm.ProvisioningState(*c.Properties.ProvisioningState)
		}
		if c.Properties != nil {
			if c.Properties.Version != nil {
				normalizeVersion(c.Properties.Version, &out.Properties.Version)
			}
			if c.Properties.DNS != nil {
				normailzeDNS(c.Properties.DNS, &out.Properties.DNS)
			}
			if c.Properties.Network != nil {
				normalizeNetwork(c.Properties.Network, &out.Properties.Network)
			}
			if c.Properties.Console != nil {
				normalizeConsole(c.Properties.Console, &out.Properties.Console)
			}
			if c.Properties.API != nil {
				normalizeAPI(c.Properties.API, &out.Properties.API)
			}
			if c.Properties.DisableUserWorkloadMonitoring != nil {
				out.Properties.DisableUserWorkloadMonitoring = *c.Properties.DisableUserWorkloadMonitoring
			}
			if c.Properties.Platform != nil {
				normalizePlatform(c.Properties.Platform, &out.Properties.Platform)
			}
			if c.Properties.IssuerURL != nil {
				out.Properties.IssuerURL = *c.Properties.IssuerURL
			}
		}
	}
}

// validateStaticComplex performs more complex, multi-field validations than
// are possible with struct tag validation. The returned CloudErrorBody slice
// contains structured but user-friendly details for all discovered errors.
func validateStaticComplex(normalized *api.HCPOpenShiftCluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody
	// Idea is to check every identity mentioned in the Identity.UserAssignedIdentities is being declared under Properties.Platform.OperatorsAuthentication.UserAssignedIdentities
	if normalized.Identity.UserAssignedIdentities != nil {
		//Initiate the map that will have the number occurence of ConstrolPlaneOperators fields .
		controlPlaneOpOccurrences := make(map[string]int)
		//Generate a Map of Resource IDs of ControlplaneOperators MI , disregard the DataPlaneOperators.
		for _, operatorResourceID := range normalized.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
			controlPlaneOpOccurrences[operatorResourceID]++
		}
		//variable to hold serviceManagedIdentity
		smiResourceID := normalized.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity

		for operatorName, resourceID := range normalized.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
			_, ok := normalized.Identity.UserAssignedIdentities[resourceID]
			if !ok {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Message: fmt.Sprintf(
						"identity %s is not assigned to this resource",
						resourceID),
					Target: fmt.Sprintf("properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[%s]", operatorName),
				})
			} else if controlPlaneOpOccurrences[resourceID] > 1 {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Message: fmt.Sprintf(
						"identity %s is used multiple times", resourceID),
					Target: fmt.Sprintf("properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[%s]", operatorName),
				})

			}
		}

		if smiResourceID != "" {
			_, ok := normalized.Identity.UserAssignedIdentities[smiResourceID]
			if !ok {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Message: fmt.Sprintf(
						"identity %s is not assigned to this resource",
						smiResourceID),
					Target: "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				})
			}
			//making sure serviceManagedIdentity is not already assigned to controlPlaneOperators
			if _, ok := controlPlaneOpOccurrences[smiResourceID]; ok {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Message: fmt.Sprintf(
						"identity %s is used multiple times", smiResourceID),
					Target: "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				})
			}
		}

		for resourceID := range normalized.Identity.UserAssignedIdentities {
			if _, ok := controlPlaneOpOccurrences[resourceID]; !ok {
				if smiResourceID != resourceID {
					errorDetails = append(errorDetails, arm.CloudErrorBody{
						Message: fmt.Sprintf(
							"identity %s is assigned to this resource but not used",
							resourceID),
						Target: "identity.UserAssignedIdentities",
					})
				}
			}
		}

	}
	return errorDetails
}

func (c *HcpOpenShiftClusterResource) ValidateStatic(current api.VersionedHCPOpenShiftCluster, updating bool, method string) *arm.CloudError {
	var normalized api.HCPOpenShiftCluster
	var errorDetails []arm.CloudErrorBody

	cloudError := arm.NewCloudError(
		http.StatusBadRequest,
		arm.CloudErrorCodeMultipleErrorsOccurred, "",
		"Content validation failed on multiple fields")
	cloudError.Details = make([]arm.CloudErrorBody, 0)

	// Pass the embedded HcpOpenShiftClusterResource so the
	// struct field names match the clusterStructTagMap keys.
	errorDetails = api.ValidateVisibility(
		c.HcpOpenShiftClusterResource,
		current.(*HcpOpenShiftClusterResource).HcpOpenShiftClusterResource,
		clusterStructTagMap, updating)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	c.Normalize(&normalized)

	errorDetails = api.ValidateRequest(validate, method, &normalized)
	if errorDetails != nil {
		cloudError.Details = append(cloudError.Details, errorDetails...)
	}

	// Proceed with complex, multi-field validation only if single-field
	// validation has passed. This avoids running further checks on data
	// we already know to be invalid and prevents the response body from
	// becoming overwhelming.
	if len(cloudError.Details) == 0 {
		errorDetails = validateStaticComplex(&normalized)
		if errorDetails != nil {
			cloudError.Details = append(cloudError.Details, errorDetails...)
		}
	}

	switch len(cloudError.Details) {
	case 0:
		cloudError = nil
	case 1:
		// Promote a single validation error out of details.
		cloudError.CloudErrorBody = &cloudError.Details[0]
	}

	return cloudError
}

func normalizeVersion(p *generated.VersionProfile, out *api.VersionProfile) {
	if p.ID != nil {
		out.ID = *p.ID
	}
	if p.ChannelGroup != nil {
		out.ChannelGroup = *p.ChannelGroup
	}
	out.AvailableUpgrades = api.StringPtrSliceToStringSlice(p.AvailableUpgrades)
}

func normailzeDNS(p *generated.DNSProfile, out *api.DNSProfile) {
	if p.BaseDomain != nil {
		out.BaseDomain = *p.BaseDomain
	}
	if p.BaseDomainPrefix != nil {
		out.BaseDomainPrefix = *p.BaseDomainPrefix
	}
}

func normalizeNetwork(p *generated.NetworkProfile, out *api.NetworkProfile) {
	if p.NetworkType != nil {
		out.NetworkType = api.NetworkType(*p.NetworkType)
	}
	if p.PodCidr != nil {
		out.PodCIDR = *p.PodCidr
	}
	if p.ServiceCidr != nil {
		out.ServiceCIDR = *p.ServiceCidr
	}
	if p.MachineCidr != nil {
		out.MachineCIDR = *p.MachineCidr
	}
	if p.HostPrefix != nil {
		out.HostPrefix = *p.HostPrefix
	}
}

func normalizeConsole(p *generated.ConsoleProfile, out *api.ConsoleProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
}

func normalizeAPI(p *generated.APIProfile, out *api.APIProfile) {
	if p.URL != nil {
		out.URL = *p.URL
	}
	if p.Visibility != nil {
		out.Visibility = api.Visibility(*p.Visibility)
	}
}

func normalizePlatform(p *generated.PlatformProfile, out *api.PlatformProfile) {
	if p.ManagedResourceGroup != nil {
		out.ManagedResourceGroup = *p.ManagedResourceGroup
	}
	if p.SubnetID != nil {
		out.SubnetID = *p.SubnetID
	}
	if p.OutboundType != nil {
		out.OutboundType = api.OutboundType(*p.OutboundType)
	}
	if p.NetworkSecurityGroupID != nil {
		out.NetworkSecurityGroupID = *p.NetworkSecurityGroupID
	}
	if p.OperatorsAuthentication != nil {
		normalizeOperatorsAuthentication(p.OperatorsAuthentication, &out.OperatorsAuthentication)
	}
}

func normalizeOperatorsAuthentication(p *generated.OperatorsAuthenticationProfile, out *api.OperatorsAuthenticationProfile) {
	if p.UserAssignedIdentities != nil {
		normalizeUserAssignedIdentities(p.UserAssignedIdentities, &out.UserAssignedIdentities)
	}
}

func normalizeUserAssignedIdentities(p *generated.UserAssignedIdentitiesProfile, out *api.UserAssignedIdentitiesProfile) {
	api.MergeStringPtrMap(p.ControlPlaneOperators, &out.ControlPlaneOperators)
	api.MergeStringPtrMap(p.DataPlaneOperators, &out.DataPlaneOperators)
	if p.ServiceManagedIdentity != nil {
		out.ServiceManagedIdentity = *p.ServiceManagedIdentity
	}
}

func normalizeIdentityUserAssignedIdentities(p map[string]*generated.UserAssignedIdentity, out *map[string]*arm.UserAssignedIdentity) {
	if *out == nil {
		*out = make(map[string]*arm.UserAssignedIdentity)
	}
	for key, value := range p {
		if value != nil {
			(*out)[key] = &arm.UserAssignedIdentity{
				ClientID:    value.ClientID,
				PrincipalID: value.PrincipalID,
			}
		}
	}
}
func convertUserAssignedIdentities(from map[string]*arm.UserAssignedIdentity) map[string]*generated.UserAssignedIdentity {
	converted := make(map[string]*generated.UserAssignedIdentity)
	for key, value := range from {
		if value != nil {
			converted[key] = &generated.UserAssignedIdentity{
				ClientID:    value.ClientID,
				PrincipalID: value.PrincipalID,
			}
		}
	}
	return converted
}
