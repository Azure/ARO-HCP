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

package serverutils

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	arm "github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/redact"
)

func TestRedactTypedDocument_RedactsClusterCreatedByAndLastModifiedBy(t *testing.T) {
	subscriptionID := "test-sub"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	resourceID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/"+resourceGroupName+"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName)
	subnetID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/vnet-1/subnets/subnet-cluster")
	vnetIntegrationSubnetID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/vnet-1/subnets/subnet-vnet-integration")
	nsgID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/network-rg/providers/Microsoft.Network/networkSecurityGroups/nsg-1")
	controlPlaneIdentityID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/identity-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cp-identity")
	dataPlaneIdentityID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/identity-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dp-identity")
	serviceManagedIdentityID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/identity-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/svc-identity")

	now := time.Now().UTC()
	createdAt := now.Add(-2 * time.Hour)
	lastModifiedAt := now.Add(-1 * time.Hour)
	deletionTimestamp := metav1.NewTime(now.Add(30 * time.Minute))
	clusterServiceDeletionTimestamp := metav1.NewTime(now.Add(45 * time.Minute))
	clientID := "identity-client-id"
	principalID := "identity-principal-id"

	clusterServiceID := mustInternalID(t, "/api/aro_hcp/v1alpha1/clusters/cs-cluster-id")

	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:        resourceID,
			ExistingCosmosUID: "legacy-cluster-cosmos-id",
			CosmosETag:        azcore.ETag("cluster-etag"),
			InstanceVersion:   7,
			PartitionKey:      strings.ToLower(subscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: clusterName,
				Type: api.ClusterResourceType.String(),
				SystemData: &arm.SystemData{
					CreatedBy:          "user@example.com",
					CreatedByType:      arm.CreatedByTypeUser,
					CreatedAt:          &createdAt,
					LastModifiedBy:     "admin@example.com",
					LastModifiedByType: arm.CreatedByTypeApplication,
					LastModifiedAt:     &lastModifiedAt,
				},
			},
			Location: "eastus",
			Tags:     map[string]string{"environment": "test", "owner": "team"},
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           "4.14.0",
				ChannelGroup: "stable",
			},
			DNS: api.CustomerDNSProfile{
				BaseDomainPrefix: "test-cluster",
			},
			Network: api.NetworkProfile{
				NetworkType: api.NetworkTypeOVNKubernetes,
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
				HostPrefix:  23,
			},
			API: api.CustomerAPIProfile{
				Visibility:      api.VisibilityPublic,
				AuthorizedCIDRs: []string{"10.0.0.0/24", "192.168.0.0/24"},
			},
			Ingress: api.CustomerIngressProfile{
				Type: api.IngressTypePublic,
			},
			Platform: api.CustomerPlatformProfile{
				ManagedResourceGroup:    "managed-rg",
				SubnetID:                subnetID,
				VnetIntegrationSubnetID: vnetIntegrationSubnetID,
				OutboundType:            api.OutboundTypeLoadBalancer,
				NetworkSecurityGroupID:  nsgID,
				OperatorsAuthentication: api.OperatorsAuthenticationProfile{
					UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
						ControlPlaneOperators:  map[string]*azcorearm.ResourceID{"cp-operator": controlPlaneIdentityID},
						DataPlaneOperators:     map[string]*azcorearm.ResourceID{"dp-operator": dataPlaneIdentityID},
						ServiceManagedIdentity: serviceManagedIdentityID,
					},
				},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:               250,
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 1800,
				PodPriorityThreshold:        -5,
			},
			NodeDrainTimeoutMinutes: 30,
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							Visibility: api.KeyVaultVisibilityPrivate,
							ActiveKey: api.KmsKey{
								Name:      "key-name",
								VaultName: "vault-name",
								Version:   "v1",
							},
						},
					},
				},
			},
			ClusterImageRegistry: api.ClusterImageRegistryProfile{
				State: api.ClusterImageRegistryStateDisabled,
			},
			ImageDigestMirrors: []api.ImageDigestMirror{
				{
					Source:             "quay.io/openshift-release-dev/ocp-release",
					Mirrors:            []string{"mirror1.example.com/ocp-release", "mirror2.example.com/ocp-release"},
					MirrorSourcePolicy: api.MirrorSourcePolicyAllowContactingSource,
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState:            arm.ProvisioningStateSucceeded,
			ClusterServiceID:             clusterServiceID,
			ActiveOperationID:            "cluster-op-123",
			RevokeCredentialsOperationID: "revoke-op-456",
			DNS: api.ServiceProviderDNSProfile{
				BaseDomain: "apps.example.com",
			},
			Console: api.ServiceProviderConsoleProfile{
				URL: "https://console.example.com",
			},
			API: api.ServiceProviderAPIProfile{
				URL: "https://api.example.com:6443",
			},
			Platform: api.ServiceProviderPlatformProfile{
				IssuerURL: "https://issuer.example.com",
			},
			ExperimentalFeatures: api.ExperimentalFeatures{
				ControlPlaneAvailability:  api.SingleReplicaControlPlane,
				ControlPlanePodSizing:     api.MinimalControlPlanePodSizing,
				ControlPlaneOperatorImage: "quay.io/custom/cpo@sha256:1234",
			},
			ManagedIdentitiesDataPlaneIdentityURL: "https://managed-identity.example.com/identity",
			ClusterUID:                            "cluster-uid-123",
			BillingDocumentCosmosID:               "billing-cosmos-id",
			DeletionTimestamp:                     &deletionTimestamp,
			ClusterServiceDeletionTimestamp:       &clusterServiceDeletionTimestamp,
			UsesNewClusterDeletionApproach:        true,
		},
		Identity: &arm.ManagedServiceIdentity{
			PrincipalID: "cluster-principal-id",
			TenantID:    "cluster-tenant-id",
			Type:        arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
			UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
				"/subscriptions/test-sub/resourceGroups/identity-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/uai1": {
					ClientID:    &clientID,
					PrincipalID: &principalID,
				},
			},
		},
		Status: api.HCPOpenShiftClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 3,
					LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					Reason:             "Provisioned",
					Message:            "cluster is ready",
				},
			},
		},
	}

	clusterBytes, err := json.Marshal(cluster)
	assert.NoError(t, err)

	doc := &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: "test-id",
		},
		PartitionKey: subscriptionID,
		ResourceID:   resourceID,
		ResourceType: api.ClusterResourceType.String(),
		Properties:   clusterBytes,
	}

	redactedDoc, err := redactTypedDocument(doc)
	assert.NoError(t, err)
	assertTypedDocumentFieldsExceptPropertiesMatch(t, doc, redactedDoc)

	// Unmarshal the redacted Properties
	var redactedCluster api.HCPOpenShiftCluster
	err = json.Unmarshal(redactedDoc.Properties, &redactedCluster)
	assert.NoError(t, err)

	expectedCluster := cloneThroughJSON(t, *cluster)
	expectedCluster.SystemData.CreatedBy = redact.RedactStrConst
	expectedCluster.SystemData.LastModifiedBy = redact.RedactStrConst

	// Verify CreatedBy is redacted
	assert.Equal(t, redact.RedactStrConst, redactedCluster.SystemData.CreatedBy, "CreatedBy should be redacted")
	// Verify LastModifiedBy is redacted
	assert.Equal(t, redact.RedactStrConst, redactedCluster.SystemData.LastModifiedBy, "LastModifiedBy should be redacted")
	// Verify resource IDs are preserved
	assert.NotNil(t, redactedDoc.ResourceID, "TypedDocument ResourceID should not be nil")
	assert.Equal(t, resourceID.String(), redactedDoc.ResourceID.String(), "TypedDocument ResourceID should not be redacted")
	assert.NotNil(t, redactedCluster.ID, "Cluster ID should not be nil")
	assert.Equal(t, resourceID.String(), redactedCluster.ID.String(), "Cluster properties.id should not be redacted")

	// Verify all other fields are NOT redacted by comparing full object.
	assert.Equal(t, expectedCluster, redactedCluster)
}

func TestRedactTypedDocument_RedactsNodePoolCreatedByAndLastModifiedBy(t *testing.T) {
	subscriptionID := "test-sub"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	nodePoolName := "nodepool-1"

	nodePoolResourceID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/"+resourceGroupName+"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName+"/nodePools/"+nodePoolName)
	nodePoolSubnetID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/vnet-1/subnets/subnet-nodepool")
	encryptionSetID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/encryption-rg/providers/Microsoft.Compute/diskEncryptionSets/des-nodepool")

	now := time.Now().UTC()
	createdAt := now.Add(-50 * time.Minute)
	lastModifiedAt := now.Add(-20 * time.Minute)
	npDeletionTimestamp := metav1.NewTime(now.Add(10 * time.Minute))
	npClusterServiceDeletionTimestamp := metav1.NewTime(now.Add(20 * time.Minute))
	clusterServiceID := mustInternalID(t, "/api/aro_hcp/v1alpha1/clusters/cs-cluster-id/node_pools/cs-nodepool-id")
	clientID := "np-client-id"
	principalID := "np-principal-id"

	nodePool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:        nodePoolResourceID,
			ExistingCosmosUID: "legacy-nodepool-cosmos-id",
			CosmosETag:        azcore.ETag("nodepool-etag"),
			InstanceVersion:   11,
			PartitionKey:      strings.ToLower(subscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodePoolResourceID,
				Name: nodePoolName,
				Type: api.NodePoolResourceType.String(),
				SystemData: &arm.SystemData{
					CreatedBy:          "user@example.com",
					CreatedByType:      arm.CreatedByTypeUser,
					CreatedAt:          &createdAt,
					LastModifiedBy:     "admin@example.com",
					LastModifiedByType: arm.CreatedByTypeApplication,
					LastModifiedAt:     &lastModifiedAt,
				},
			},
			Location: "eastus",
			Tags:     map[string]string{"environment": "test", "owner": "team"},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			Version: api.NodePoolVersionProfile{
				ID:           "4.14.0",
				ChannelGroup: "stable",
			},
			Platform: api.NodePoolPlatformProfile{
				SubnetID:               nodePoolSubnetID,
				VMSize:                 "Standard_D8s_v5",
				EnableEncryptionAtHost: true,
				OSDisk: api.OSDiskProfile{
					SizeGiB:                int32Ptr(1024),
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
					EncryptionSetID:        encryptionSetID,
					DiskType:               api.OsDiskTypeManaged,
				},
				AvailabilityZone: "1",
			},
			Replicas:   3,
			AutoRepair: true,
			AutoScaling: &api.NodePoolAutoScaling{
				Min: 2,
				Max: 10,
			},
			Labels: map[string]string{"node-role.kubernetes.io/worker": "", "workload": "apps"},
			Taints: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "dedicated", Value: "gpu"},
				{Effect: api.EffectPreferNoSchedule, Key: "arch", Value: "arm64"},
			},
			NodeDrainTimeoutMinutes: int32Ptr(45),
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID:                clusterServiceID,
			ActiveOperationID:               "np-op-123",
			DeletionTimestamp:               &npDeletionTimestamp,
			ClusterServiceDeletionTimestamp: &npClusterServiceDeletionTimestamp,
			UsesNewNodePoolDeletionApproach: true,
		},
		Identity: &arm.ManagedServiceIdentity{
			PrincipalID: "nodepool-principal-id",
			TenantID:    "nodepool-tenant-id",
			Type:        arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
			UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
				"/subscriptions/test-sub/resourceGroups/identity-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/np-uai1": {
					ClientID:    &clientID,
					PrincipalID: &principalID,
				},
			},
		},
		Status: api.HCPOpenShiftClusterNodePoolStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Scaling",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: 2,
					LastTransitionTime: metav1.NewTime(now.Add(-5 * time.Minute)),
					Reason:             "AtDesiredReplicas",
					Message:            "nodepool replicas match desired state",
				},
			},
		},
	}

	nodePoolBytes, err := json.Marshal(nodePool)
	assert.NoError(t, err)

	doc := &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: "test-nodepool-id",
		},
		PartitionKey: subscriptionID,
		ResourceID:   nodePoolResourceID,
		ResourceType: api.NodePoolResourceType.String(),
		Properties:   nodePoolBytes,
	}

	redactedDoc, err := redactTypedDocument(doc)
	assert.NoError(t, err)
	assertTypedDocumentFieldsExceptPropertiesMatch(t, doc, redactedDoc)

	// Parse redacted TypedDocument and strongly-typed NodePool properties.
	var redactedNodePool api.HCPOpenShiftClusterNodePool
	err = json.Unmarshal(redactedDoc.Properties, &redactedNodePool)
	assert.NoError(t, err)

	expectedNodePool := cloneThroughJSON(t, *nodePool)
	expectedNodePool.SystemData.CreatedBy = redact.RedactStrConst
	expectedNodePool.SystemData.LastModifiedBy = redact.RedactStrConst

	// Verify CreatedBy and LastModifiedBy are redacted.
	assert.Equal(t, redact.RedactStrConst, redactedNodePool.SystemData.CreatedBy, "CreatedBy should be redacted")
	assert.Equal(t, redact.RedactStrConst, redactedNodePool.SystemData.LastModifiedBy, "LastModifiedBy should be redacted")
	// Verify resource IDs are preserved
	assert.NotNil(t, redactedDoc.ResourceID, "TypedDocument ResourceID should not be nil")
	assert.Equal(t, nodePoolResourceID.String(), redactedDoc.ResourceID.String(), "TypedDocument resourceID should not be redacted")
	assert.NotNil(t, redactedNodePool.ID, "NodePool properties.id should not be nil")
	assert.Equal(t, nodePoolResourceID.String(), redactedNodePool.ID.String(), "NodePool properties.id should not be redacted")

	// Verify all other fields are NOT redacted by comparing full object.
	assert.Equal(t, expectedNodePool, redactedNodePool)
}

func TestRedactTypedDocument_RedactsExternalAuthCreatedByAndLastModifiedBy(t *testing.T) {
	subscriptionID := "test-sub"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	externalAuthName := "external-auth-1"

	externalAuthResourceID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/resourceGroups/"+resourceGroupName+"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName+"/externalAuths/"+externalAuthName)

	now := time.Now().UTC()
	createdAt := now.Add(-40 * time.Minute)
	lastModifiedAt := now.Add(-10 * time.Minute)
	eaDeletionTimestamp := metav1.NewTime(now.Add(5 * time.Minute))
	eaClusterServiceDeletionTimestamp := metav1.NewTime(now.Add(15 * time.Minute))
	clusterServiceID := mustInternalID(t, "/api/aro_hcp/v1alpha1/clusters/cs-cluster-id/external_auth_config/external_auths/cs-external-auth-id")

	externalAuth := &api.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:        externalAuthResourceID,
			ExistingCosmosUID: "legacy-external-auth-cosmos-id",
			CosmosETag:        azcore.ETag("external-auth-etag"),
			InstanceVersion:   13,
			PartitionKey:      strings.ToLower(subscriptionID),
		},
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   externalAuthResourceID,
				Name: externalAuthName,
				Type: api.ExternalAuthResourceType.String(),
				SystemData: &arm.SystemData{
					CreatedBy:          "user@example.com",
					CreatedByType:      arm.CreatedByTypeUser,
					CreatedAt:          &createdAt,
					LastModifiedBy:     "admin@example.com",
					LastModifiedByType: arm.CreatedByTypeApplication,
					LastModifiedAt:     &lastModifiedAt,
				},
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			Issuer: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"aud-1", "aud-2"},
				CA:        "-----BEGIN CERTIFICATE-----test-----END CERTIFICATE-----",
			},
			Clients: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "kube-apiserver",
						AuthClientNamespace: "openshift-kube-apiserver",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"scope-a", "scope-b"},
					Type:        api.ExternalAuthClientTypeConfidential,
				},
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "oauth-server",
						AuthClientNamespace: "openshift-authentication",
					},
					ClientID:    "client-id-2",
					ExtraScopes: []string{"scope-c"},
					Type:        api.ExternalAuthClientTypePublic,
				},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "preferred_username",
						Prefix:       "idp:",
						PrefixPolicy: api.UsernameClaimPrefixPolicyPrefix,
					},
					Groups: &api.GroupClaimProfile{
						Claim:  "groups",
						Prefix: "group:",
					},
				},
				ValidationRules: []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "tid",
							RequiredValue: "tenant-id-123",
						},
					},
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "https://issuer.example.com",
						},
					},
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID:                    clusterServiceID,
			ActiveOperationID:                   "external-auth-op-123",
			DeletionTimestamp:                   &eaDeletionTimestamp,
			ClusterServiceDeletionTimestamp:     &eaClusterServiceDeletionTimestamp,
			UsesNewExternalAuthDeletionApproach: true,
		},
		Status: api.HCPOpenShiftClusterExternalAuthStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 5,
					LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					Reason:             "Configured",
					Message:            "external auth is configured",
				},
			},
		},
	}

	externalAuthBytes, err := json.Marshal(externalAuth)
	assert.NoError(t, err)

	doc := &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: "test-external-auth-id",
		},
		PartitionKey: subscriptionID,
		ResourceID:   externalAuthResourceID,
		ResourceType: api.ExternalAuthResourceType.String(),
		Properties:   externalAuthBytes,
	}

	redactedDoc, err := redactTypedDocument(doc)
	assert.NoError(t, err)
	assertTypedDocumentFieldsExceptPropertiesMatch(t, doc, redactedDoc)

	var redactedExternalAuth api.HCPOpenShiftClusterExternalAuth
	err = json.Unmarshal(redactedDoc.Properties, &redactedExternalAuth)
	assert.NoError(t, err)

	expectedExternalAuth := cloneThroughJSON(t, *externalAuth)
	expectedExternalAuth.SystemData.CreatedBy = redact.RedactStrConst
	expectedExternalAuth.SystemData.LastModifiedBy = redact.RedactStrConst

	assert.Equal(t, redact.RedactStrConst, redactedExternalAuth.SystemData.CreatedBy, "CreatedBy should be redacted")
	assert.Equal(t, redact.RedactStrConst, redactedExternalAuth.SystemData.LastModifiedBy, "LastModifiedBy should be redacted")
	assert.NotNil(t, redactedDoc.ResourceID, "TypedDocument ResourceID should not be nil")
	assert.Equal(t, externalAuthResourceID.String(), redactedDoc.ResourceID.String(), "TypedDocument resourceID should not be redacted")
	assert.NotNil(t, redactedExternalAuth.ID, "ExternalAuth properties.id should not be nil")
	assert.Equal(t, externalAuthResourceID.String(), redactedExternalAuth.ID.String(), "ExternalAuth properties.id should not be redacted")

	assert.Equal(t, expectedExternalAuth, redactedExternalAuth)
}

func TestRedactTypedDocument_RedactsVersionCreatedByAndLastModifiedBy(t *testing.T) {
	subscriptionID := "test-sub"
	location := "eastus"
	versionName := "4.14.21"

	versionResourceID := mustParseResourceID(t, "/subscriptions/"+subscriptionID+"/providers/Microsoft.RedHatOpenShift/locations/"+location+"/hcpOpenShiftVersions/"+versionName)

	now := time.Now().UTC()
	createdAt := now.Add(-3 * time.Hour)
	lastModifiedAt := now.Add(-90 * time.Minute)

	version := &api.HCPOpenShiftVersion{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   versionResourceID,
				Name: versionName,
				Type: api.VersionResourceType.String(),
				SystemData: &arm.SystemData{
					CreatedBy:          "user@example.com",
					CreatedByType:      arm.CreatedByTypeUser,
					CreatedAt:          &createdAt,
					LastModifiedBy:     "admin@example.com",
					LastModifiedByType: arm.CreatedByTypeApplication,
					LastModifiedAt:     &lastModifiedAt,
				},
			},
		},
		Properties: api.HCPOpenShiftVersionProperties{
			ChannelGroup:       "stable",
			Enabled:            true,
			EndOfLifeTimestamp: now.Add(365 * 24 * time.Hour).UTC().Truncate(time.Second),
		},
	}

	versionBytes, err := json.Marshal(version)
	assert.NoError(t, err)

	doc := &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: "test-version-id",
		},
		PartitionKey: strings.ToLower(subscriptionID),
		ResourceID:   versionResourceID,
		ResourceType: api.VersionResourceType.String(),
		Properties:   versionBytes,
	}

	redactedDoc, err := redactTypedDocument(doc)
	assert.NoError(t, err)
	assertTypedDocumentFieldsExceptPropertiesMatch(t, doc, redactedDoc)

	var redactedVersion api.HCPOpenShiftVersion
	err = json.Unmarshal(redactedDoc.Properties, &redactedVersion)
	assert.NoError(t, err)

	expectedVersion := cloneThroughJSON(t, *version)
	expectedVersion.SystemData.CreatedBy = redact.RedactStrConst
	expectedVersion.SystemData.LastModifiedBy = redact.RedactStrConst

	assert.Equal(t, redact.RedactStrConst, redactedVersion.SystemData.CreatedBy, "CreatedBy should be redacted")
	assert.Equal(t, redact.RedactStrConst, redactedVersion.SystemData.LastModifiedBy, "LastModifiedBy should be redacted")
	assert.NotNil(t, redactedDoc.ResourceID, "TypedDocument ResourceID should not be nil")
	assert.Equal(t, versionResourceID.String(), redactedDoc.ResourceID.String(), "TypedDocument resourceID should not be redacted")
	assert.NotNil(t, redactedVersion.ID, "Version properties.id should not be nil")
	assert.Equal(t, versionResourceID.String(), redactedVersion.ID.String(), "Version properties.id should not be redacted")

	assert.Equal(t, expectedVersion, redactedVersion)
}

func TestRedactTypedDocument_NonClusterDocumentNotModified(t *testing.T) {
	subscriptionID := "test-sub"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	resourceID, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroupName + "/providers/Microsoft.RedHatOpenShift/hostingEnvironments/" + clusterName)
	assert.NoError(t, err)

	properties := json.RawMessage(`{"status":"Succeeded"}`)

	doc := &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: "operation-id",
		},
		PartitionKey: subscriptionID,
		ResourceID:   resourceID,
		ResourceType: "Operation",
		Properties:   properties,
	}

	redactedDoc, err := redactTypedDocument(doc)
	assert.NoError(t, err)
	assert.Same(t, doc, redactedDoc, "non-redacted documents should return the original object")
	assertTypedDocumentFieldsExceptPropertiesMatch(t, doc, redactedDoc)

	// Verify key fields are present and not modified
	assert.Equal(t, doc.ID, redactedDoc.ID, "ID should not be modified")
	assert.Equal(t, doc.PartitionKey, redactedDoc.PartitionKey, "PartitionKey should not be modified")
	assert.Equal(t, doc.ResourceType, redactedDoc.ResourceType, "ResourceType should not be modified")
	assert.Equal(t, string(doc.Properties), string(redactedDoc.Properties), "Properties content should not be modified")

}

func mustParseResourceID(t *testing.T, resourceID string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(resourceID)
	assert.NoError(t, err)
	return id
}

func mustInternalID(t *testing.T, value string) *api.InternalID {
	t.Helper()
	internalID, err := api.NewInternalID(value)
	assert.NoError(t, err)
	return &internalID
}

func cloneThroughJSON[T any](t *testing.T, in T) T {
	t.Helper()
	b, err := json.Marshal(in)
	assert.NoError(t, err)

	var out T
	err = json.Unmarshal(b, &out)
	assert.NoError(t, err)
	return out
}

func assertTypedDocumentFieldsExceptPropertiesMatch(t *testing.T, expected *database.TypedDocument, actual *database.TypedDocument) {
	t.Helper()

	expectedClone := cloneThroughJSON(t, *expected)
	actualClone := cloneThroughJSON(t, *actual)

	expectedClone.Properties = nil
	actualClone.Properties = nil

	assert.Equal(t, expectedClone, actualClone, "all TypedDocument fields except properties should match")
}

func int32Ptr(v int32) *int32 {
	return &v
}
