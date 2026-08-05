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

package operationtesting

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// MustParseTime parses a time string in RFC3339 format and panics on error.
// Use for test constants to make date values more readable.
func MustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// Common test constants
const (
	TestSubscriptionID            = "00000000-0000-0000-0000-000000000000"
	TestResourceGroupName         = "test-rg"
	TestClusterName               = "test-cluster"
	TestNodePoolName              = "test-nodepool"
	TestExternalAuthName          = "test-external-auth"
	TestClusterServiceIDStr       = "/api/clusters_mgmt/v1/clusters/abc123"
	TestNodePoolIDStr             = "/api/aro_hcp/v1alpha1/clusters/abc123/node_pools/test-nodepool"
	TestExternalAuthIDStr         = "/api/clusters_mgmt/v1/clusters/abc123/external_auth_config/external_auths/ea123"
	TestBreakGlassCredentialIDStr = "/api/clusters_mgmt/v1/clusters/abc123/break_glass_credentials/bgc123"
	TestOperationName             = "test-operation-id"
	TestTenantID                  = "11111111-1111-1111-1111-111111111111"
	TestAzureLocation             = "eastus"
	TestClusterUID                = "00000000-0000-0000-0000-000000000000"
)

// ClusterTestFixture contains common test objects for cluster operations
type ClusterTestFixture struct {
	ClusterResourceID         *azcorearm.ResourceID
	OperationID               *azcorearm.ResourceID
	CosmosOperationResourceID *azcorearm.ResourceID
	ClusterInternalID         metadataapi.InternalID
}

func NewClusterTestFixture() *ClusterTestFixture {
	return &ClusterTestFixture{
		ClusterResourceID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/resourceGroups/" + TestResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + TestClusterName,
		)),
		OperationID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + TestAzureLocation +
				"/operationstatuses/" + TestOperationName,
		)),
		CosmosOperationResourceID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + TestOperationName,
		)),
		ClusterInternalID: metadataapi.Must(metadataapi.NewInternalID(TestClusterServiceIDStr)),
	}
}

func (f *ClusterTestFixture) NewCluster(createdAt *time.Time) *coreapi.HCPOpenShiftCluster {
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   f.ClusterResourceID,
			PartitionKey: strings.ToLower(f.ClusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   f.ClusterResourceID,
				Name: TestClusterName,
				Type: f.ClusterResourceID.ResourceType.String(),
				SystemData: &coreapi.SystemData{
					CreatedAt: createdAt,
				},
			},
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Etcd: coreapi.EtcdProfile{
				DataEncryption: coreapi.EtcdDataEncryptionProfile{
					KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
						EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
						Kms: &coreapi.KmsEncryptionProfile{
							Visibility: metadataapi.KeyVaultVisibilityPublic,
							ActiveKey: coreapi.KmsKey{
								Name:      "test-key",
								VaultName: "test-vault",
								Version:   "v1",
							},
						},
					},
				},
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID:  &f.ClusterInternalID,
			ActiveOperationID: TestOperationName,
			ClusterUID:        TestClusterUID,
		},
	}
}

func (f *ClusterTestFixture) NewOperation(request cosmosstorageutils.OperationRequest) *coreapi.Operation {
	return &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   f.CosmosOperationResourceID,
			PartitionKey: strings.ToLower(f.CosmosOperationResourceID.SubscriptionID),
		},
		TenantID:    TestTenantID,
		Status:      coreapi.ProvisioningStateAccepted,
		Request:     request,
		ExternalID:  f.ClusterResourceID,
		InternalID:  f.ClusterInternalID,
		OperationID: f.OperationID,
	}
}

func (f *ClusterTestFixture) OperationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID:   TestSubscriptionID,
		OperationName:    TestOperationName,
		ParentResourceID: f.ClusterResourceID.String(),
	}
}

// NodePoolTestFixture contains common test objects for node pool operations
type NodePoolTestFixture struct {
	ClusterResourceID         *azcorearm.ResourceID
	NodePoolResourceID        *azcorearm.ResourceID
	OperationID               *azcorearm.ResourceID
	CosmosOperationResourceID *azcorearm.ResourceID
	ClusterInternalID         metadataapi.InternalID
	NodePoolInternalID        metadataapi.InternalID
}

func NewNodePoolTestFixture() *NodePoolTestFixture {
	ClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + TestSubscriptionID +
			"/resourceGroups/" + TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + TestClusterName,
	))
	return &NodePoolTestFixture{
		ClusterResourceID: ClusterResourceID,
		NodePoolResourceID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/resourceGroups/" + TestResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + TestClusterName +
				"/nodePools/" + TestNodePoolName,
		)),
		OperationID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + TestAzureLocation +
				"/operationstatuses/" + TestOperationName,
		)),
		CosmosOperationResourceID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + TestOperationName,
		)),
		ClusterInternalID:  metadataapi.Must(metadataapi.NewInternalID(TestClusterServiceIDStr)),
		NodePoolInternalID: metadataapi.Must(metadataapi.NewInternalID(TestNodePoolIDStr)),
	}
}

func (f *NodePoolTestFixture) NewCluster() *coreapi.HCPOpenShiftCluster {
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   f.ClusterResourceID,
			PartitionKey: strings.ToLower(f.ClusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   f.ClusterResourceID,
				Name: TestClusterName,
				Type: f.ClusterResourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &f.ClusterInternalID,
		},
	}
}

func (f *NodePoolTestFixture) NewNodePool() *coreapi.HCPOpenShiftClusterNodePool {
	return &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: f.NodePoolResourceID, PartitionKey: strings.ToLower(f.NodePoolResourceID.SubscriptionID)},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   f.NodePoolResourceID,
				Name: TestNodePoolName,
				Type: f.NodePoolResourceID.ResourceType.String(),
			},
		},
		Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: coreapi.ProvisioningStateAccepted,
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID:  &f.NodePoolInternalID,
			ActiveOperationID: TestOperationName,
		},
	}
}

func (f *NodePoolTestFixture) NewServiceProviderNodePool() *coreapi.ServiceProviderNodePool {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
		f.NodePoolResourceID.String(),
		coreapi.ServiceProviderNodePoolResourceTypeName,
		coreapi.ServiceProviderNodePoolResourceName,
	)))
	return &coreapi.ServiceProviderNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func (f *NodePoolTestFixture) NewNodePoolVersionController(conditions []metav1.Condition) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		f.NodePoolResourceID.String() + "/hcpOpenShiftControllers/NodePoolVersion",
	))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: f.NodePoolResourceID,
		Status: coreapi.ControllerStatus{
			Conditions: conditions,
		},
	}
}

func (f *NodePoolTestFixture) NewOperation(request cosmosstorageutils.OperationRequest) *coreapi.Operation {
	return &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   f.CosmosOperationResourceID,
			PartitionKey: strings.ToLower(f.CosmosOperationResourceID.SubscriptionID),
		},
		TenantID:    TestTenantID,
		Status:      coreapi.ProvisioningStateAccepted,
		Request:     request,
		ExternalID:  f.NodePoolResourceID,
		InternalID:  f.NodePoolInternalID,
		OperationID: f.OperationID,
	}
}

func (f *NodePoolTestFixture) OperationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID:   TestSubscriptionID,
		OperationName:    TestOperationName,
		ParentResourceID: f.NodePoolResourceID.String(),
	}
}

// ExternalAuthTestFixture contains common test objects for external auth operations
type ExternalAuthTestFixture struct {
	ClusterResourceID         *azcorearm.ResourceID
	ExternalAuthResourceID    *azcorearm.ResourceID
	OperationID               *azcorearm.ResourceID
	CosmosOperationResourceID *azcorearm.ResourceID
	ClusterInternalID         metadataapi.InternalID
	ExternalAuthInternalID    metadataapi.InternalID
}

func NewExternalAuthTestFixture() *ExternalAuthTestFixture {
	ClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + TestSubscriptionID +
			"/resourceGroups/" + TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + TestClusterName,
	))
	return &ExternalAuthTestFixture{
		ClusterResourceID: ClusterResourceID,
		ExternalAuthResourceID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/resourceGroups/" + TestResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + TestClusterName +
				"/externalAuths/" + TestExternalAuthName,
		)),
		OperationID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + TestAzureLocation +
				"/operationstatuses/" + TestOperationName,
		)),
		CosmosOperationResourceID: metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + TestSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + TestOperationName,
		)),
		ClusterInternalID:      metadataapi.Must(metadataapi.NewInternalID(TestClusterServiceIDStr)),
		ExternalAuthInternalID: metadataapi.Must(metadataapi.NewInternalID(TestExternalAuthIDStr)),
	}
}

func (f *ExternalAuthTestFixture) NewCluster() *coreapi.HCPOpenShiftCluster {
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   f.ClusterResourceID,
			PartitionKey: strings.ToLower(f.ClusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   f.ClusterResourceID,
				Name: TestClusterName,
				Type: f.ClusterResourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &f.ClusterInternalID,
		},
	}
}

func (f *ExternalAuthTestFixture) NewExternalAuth() *coreapi.HCPOpenShiftClusterExternalAuth {
	return &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: f.ExternalAuthResourceID, PartitionKey: strings.ToLower(f.ExternalAuthResourceID.SubscriptionID)},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   f.ExternalAuthResourceID,
				Name: TestExternalAuthName,
				Type: f.ExternalAuthResourceID.ResourceType.String(),
			},
		},
		Properties: coreapi.HCPOpenShiftClusterExternalAuthProperties{
			ProvisioningState: coreapi.ProvisioningStateAccepted,
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID:  &f.ExternalAuthInternalID,
			ActiveOperationID: TestOperationName,
		},
	}
}

func (f *ExternalAuthTestFixture) NewOperation(request cosmosstorageutils.OperationRequest) *coreapi.Operation {
	return &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   f.CosmosOperationResourceID,
			PartitionKey: strings.ToLower(f.CosmosOperationResourceID.SubscriptionID),
		},
		TenantID:    TestTenantID,
		Status:      coreapi.ProvisioningStateAccepted,
		Request:     request,
		ExternalID:  f.ExternalAuthResourceID,
		InternalID:  f.ExternalAuthInternalID,
		OperationID: f.OperationID,
	}
}

func (f *ExternalAuthTestFixture) OperationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID:   TestSubscriptionID,
		OperationName:    TestOperationName,
		ParentResourceID: f.ExternalAuthResourceID.String(),
	}
}

// ClusterUpdateMatchingHostedClusterSpec returns a HostedCluster spec that matches the
// default cluster fixture for cluster update state calculation tests.
func ClusterUpdateMatchingHostedClusterSpec() v1beta1.HostedClusterSpec {
	return v1beta1.HostedClusterSpec{
		Autoscaling: v1beta1.ClusterAutoscaling{
			MaxNodesTotal:        ptr.To[int32](0),
			MaxPodGracePeriod:    ptr.To[int32](0),
			MaxNodeProvisionTime: "0m",
			PodPriorityThreshold: ptr.To[int32](0),
		},
		ControllerAvailabilityPolicy:     v1beta1.HighlyAvailable,
		InfrastructureAvailabilityPolicy: v1beta1.HighlyAvailable,
		SecretEncryption: &v1beta1.SecretEncryptionSpec{
			Type: v1beta1.KMS,
			KMS: &v1beta1.KMSSpec{
				Azure: &v1beta1.AzureKMSSpec{
					ActiveKey: v1beta1.AzureKMSKey{
						KeyVersion: "v1",
					},
				},
			},
		},
	}
}

// NewExternalAuthUpdateTestExternalAuth returns an external auth whose properties match
// ExternalAuthUpdateMatchingOIDCProvider for external auth update state calculation tests.
func NewExternalAuthUpdateTestExternalAuth(mutate ...func(*coreapi.HCPOpenShiftClusterExternalAuth)) *coreapi.HCPOpenShiftClusterExternalAuth {
	externalAuth := NewExternalAuthTestFixture().NewExternalAuth()
	externalAuth.Properties = coreapi.HCPOpenShiftClusterExternalAuthProperties{
		ProvisioningState: coreapi.ProvisioningStateAccepted,
		Issuer: coreapi.TokenIssuerProfile{
			URL:       "https://issuer.example.com",
			Audiences: []string{"aud1", "aud2"},
			CA:        "test-ca-cert",
		},
		Clients: []coreapi.ExternalAuthClientProfile{
			{
				Component: coreapi.ExternalAuthClientComponentProfile{
					Name:                "console",
					AuthClientNamespace: "openshift-console",
				},
				ClientID:    "client-id-1",
				ExtraScopes: []string{"email", "profile"},
				Type:        metadataapi.ExternalAuthClientTypePublic,
			},
		},
		Claim: coreapi.ExternalAuthClaimProfile{
			Mappings: coreapi.TokenClaimMappingsProfile{
				Username: coreapi.UsernameClaimProfile{
					Claim:        "email",
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyNoPrefix,
				},
				Groups: &coreapi.GroupClaimProfile{
					Claim:  "groups",
					Prefix: "oidc:",
				},
			},
			ValidationRules: []coreapi.TokenClaimValidationRule{
				{
					Type: metadataapi.TokenValidationRuleTypeRequiredClaim,
					RequiredClaim: coreapi.TokenRequiredClaim{
						Claim:         "hd",
						RequiredValue: "example.com",
					},
				},
			},
		},
	}
	for _, fn := range mutate {
		if fn != nil {
			fn(externalAuth)
		}
	}
	return externalAuth
}

// ExternalAuthUpdateMatchingOIDCProvider returns an OIDCProvider that matches
// NewExternalAuthUpdateTestExternalAuth for external auth update state calculation tests.
func ExternalAuthUpdateMatchingOIDCProvider() configv1.OIDCProvider {
	return configv1.OIDCProvider{
		Name: strings.ToLower(TestExternalAuthName),
		Issuer: configv1.TokenIssuer{
			URL:       "https://issuer.example.com",
			Audiences: []configv1.TokenAudience{"aud1", "aud2"},
		},
		OIDCClients: []configv1.OIDCClientConfig{
			{
				ComponentName:      "console",
				ComponentNamespace: "openshift-console",
				ClientID:           "client-id-1",
				ExtraScopes:        []string{"email", "profile"},
			},
		},
		ClaimMappings: configv1.TokenClaimMappings{
			Username: configv1.UsernameClaimMapping{
				Claim:        "email",
				PrefixPolicy: configv1.NoPrefix,
			},
			Groups: configv1.PrefixedClaimMapping{
				TokenClaimMapping: configv1.TokenClaimMapping{
					Claim: "groups",
				},
				Prefix: "oidc:",
			},
		},
		ClaimValidationRules: []configv1.TokenClaimValidationRule{
			{
				Type: configv1.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: &configv1.TokenRequiredClaim{
					Claim:         "hd",
					RequiredValue: "example.com",
				},
			},
		},
	}
}

// ExternalAuthUpdateMatchingHostedClusterSpec returns a HostedCluster spec that matches
// NewExternalAuthUpdateTestExternalAuth for external auth update state calculation tests.
func ExternalAuthUpdateMatchingHostedClusterSpec() v1beta1.HostedClusterSpec {
	return v1beta1.HostedClusterSpec{
		Configuration: &v1beta1.ClusterConfiguration{
			Authentication: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					ExternalAuthUpdateMatchingOIDCProvider(),
				},
			},
		},
	}
}
