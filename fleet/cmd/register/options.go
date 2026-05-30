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

package register

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/internal/database"
)

// provisionShardNamespaceUUID is the namespace UUID used to generate deterministic
// provision shard IDs via UUID v5. Must match the value used by cluster-service.
var provisionShardNamespaceUUID = uuid.MustParse("916f9976-e1c0-4afd-b84c-5d5c94fbeaed")

type RawRegisterOptions struct {
	CloudEnvironment                                     string
	CosmosURL                                            string
	CosmosName                                           string
	StampIdentifier                                      string
	AutoApprove                                          bool
	SchedulingPolicy                                     string
	AKSResourceID                                        string
	PublicDNSZoneResourceID                              string
	HostedClustersSecretsKeyVaultURL                     string
	HostedClustersManagedIdentitiesKeyVaultURL           string
	HostedClustersSecretsKeyVaultManagedIdentityClientID string
	MaestroConsumerName                                  string
	MaestroRESTAPIURL                                    string
	MaestroGRPCTarget                                    string
	KubeApplierCosmosContainerName                       string
}

func DefaultRegisterOptions() *RawRegisterOptions {
	return &RawRegisterOptions{}
}

func BindRegisterOptions(opts *RawRegisterOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.CloudEnvironment, "cloud-environment", opts.CloudEnvironment, "Azure cloud environment (AzurePublicCloud, AzureChinaCloud, AzureUSGovernmentCloud)")
	cmd.Flags().StringVar(&opts.CosmosURL, "cosmos-url", opts.CosmosURL, "CosmosDB endpoint URL")
	cmd.Flags().StringVar(&opts.CosmosName, "cosmos-name", opts.CosmosName, "CosmosDB database name")
	cmd.Flags().StringVar(&opts.StampIdentifier, "stamp-identifier", opts.StampIdentifier, "stamp identifier")
	cmd.Flags().BoolVar(&opts.AutoApprove, "auto-approve", opts.AutoApprove, "automatically approve the stamp during registration")
	cmd.Flags().StringVar(&opts.SchedulingPolicy, "scheduling-policy", opts.SchedulingPolicy, "scheduling policy (Schedulable or Unschedulable)")
	cmd.Flags().StringVar(&opts.AKSResourceID, "aks-resource-id", opts.AKSResourceID, "AKS cluster ARM resource ID")
	cmd.Flags().StringVar(&opts.PublicDNSZoneResourceID, "public-dns-zone-resource-id", opts.PublicDNSZoneResourceID, "public DNS zone ARM resource ID")
	cmd.Flags().StringVar(&opts.HostedClustersSecretsKeyVaultURL, "hosted-clusters-secrets-keyvault-url", opts.HostedClustersSecretsKeyVaultURL, "key vault URL for hosted cluster secrets")
	cmd.Flags().StringVar(&opts.HostedClustersManagedIdentitiesKeyVaultURL, "hosted-clusters-managed-identities-keyvault-url", opts.HostedClustersManagedIdentitiesKeyVaultURL, "key vault URL for hosted cluster managed identities")
	cmd.Flags().StringVar(&opts.HostedClustersSecretsKeyVaultManagedIdentityClientID, "hosted-clusters-secrets-keyvault-mi-client-id", opts.HostedClustersSecretsKeyVaultManagedIdentityClientID, "client ID of the managed identity for the secrets key vault")
	cmd.Flags().StringVar(&opts.MaestroConsumerName, "maestro-consumer-name", opts.MaestroConsumerName, "Maestro consumer name")
	cmd.Flags().StringVar(&opts.MaestroRESTAPIURL, "maestro-rest-api-url", opts.MaestroRESTAPIURL, "Maestro REST API URL")
	cmd.Flags().StringVar(&opts.MaestroGRPCTarget, "maestro-grpc-target", opts.MaestroGRPCTarget, "Maestro gRPC dial target (host:port)")
	cmd.Flags().StringVar(&opts.KubeApplierCosmosContainerName, "kube-applier-cosmos-container-name", opts.KubeApplierCosmosContainerName, "Cosmos container name for kube-applier manifests")

	for _, flag := range []string{
		"cloud-environment",
		"cosmos-url",
		"cosmos-name",
		"stamp-identifier",
		"scheduling-policy",
		"aks-resource-id",
		"public-dns-zone-resource-id",
		"hosted-clusters-secrets-keyvault-url",
		"hosted-clusters-managed-identities-keyvault-url",
		"hosted-clusters-secrets-keyvault-mi-client-id",
		"maestro-consumer-name",
		"maestro-rest-api-url",
		"maestro-grpc-target",
		"kube-applier-cosmos-container-name",
	} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return err
		}
	}

	return nil
}

type validatedRegisterOptions struct {
	*RawRegisterOptions
	cloudConfiguration          cloud.Configuration
	stampResourceID             *azcorearm.ResourceID
	managementClusterResourceID *azcorearm.ResourceID
	aksResourceID               *azcorearm.ResourceID
	publicDNSZoneResourceID     *azcorearm.ResourceID
	provisionShardID            *api.InternalID
	schedulingPolicy            fleet.ManagementClusterSchedulingPolicy
}

type ValidatedRegisterOptions struct {
	*validatedRegisterOptions
}

func (o *RawRegisterOptions) Validate(ctx context.Context) (*ValidatedRegisterOptions, error) {
	cloudConfig, err := azsdk.CloudConfigurationFromName(o.CloudEnvironment)
	if err != nil {
		return nil, fmt.Errorf("--cloud-environment: %w", err)
	}

	stampResourceID, err := fleet.ToStampResourceID(o.StampIdentifier)
	if err != nil {
		return nil, fmt.Errorf("invalid stamp identifier %q: %w", o.StampIdentifier, err)
	}
	managementClusterResourceID, err := fleet.ToManagementClusterResourceID(o.StampIdentifier)
	if err != nil {
		return nil, fmt.Errorf("invalid stamp identifier %q: %w", o.StampIdentifier, err)
	}

	schedulingPolicy := fleet.ManagementClusterSchedulingPolicy(o.SchedulingPolicy)
	if !fleet.ValidManagementClusterSchedulingPolicies.Has(schedulingPolicy) {
		return nil, fmt.Errorf("invalid scheduling policy %q: must be Schedulable or Unschedulable", o.SchedulingPolicy)
	}

	aksID, err := azcorearm.ParseResourceID(o.AKSResourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid aks-resource-id: %w", err)
	}

	dnsID, err := azcorearm.ParseResourceID(o.PublicDNSZoneResourceID)
	if err != nil {
		return nil, fmt.Errorf("invalid public-dns-zone-resource-id: %w", err)
	}

	// Deterministic UUID v5 matching cluster-service/Makefile's PROVISION_SHARD_ID
	// generation. Both use the AKS cluster name as input — case-sensitive, so
	// casing must stay consistent across config sources.
	shardUUID := uuid.NewSHA1(provisionShardNamespaceUUID, []byte(aksID.Name))
	shardID, err := api.NewInternalID(fmt.Sprintf("/api/aro_hcp/v1alpha1/provision_shards/%s", shardUUID.String()))
	if err != nil {
		return nil, fmt.Errorf("failed to construct provision shard ID: %w", err)
	}

	return &ValidatedRegisterOptions{
		validatedRegisterOptions: &validatedRegisterOptions{
			RawRegisterOptions:          o,
			cloudConfiguration:          cloudConfig,
			stampResourceID:             stampResourceID,
			managementClusterResourceID: managementClusterResourceID,
			aksResourceID:               aksID,
			publicDNSZoneResourceID:     dnsID,
			provisionShardID:            &shardID,
			schedulingPolicy:            schedulingPolicy,
		},
	}, nil
}

type registerOptions struct {
	fleetDBClient                                        database.FleetDBClient
	stampIdentifier                                      string
	stampResourceID                                      *azcorearm.ResourceID
	managementClusterResourceID                          *azcorearm.ResourceID
	autoApprove                                          bool
	schedulingPolicy                                     fleet.ManagementClusterSchedulingPolicy
	aksResourceID                                        *azcorearm.ResourceID
	publicDNSZoneResourceID                              *azcorearm.ResourceID
	hostedClustersSecretsKeyVaultURL                     string
	hostedClustersManagedIdentitiesKeyVaultURL           string
	hostedClustersSecretsKeyVaultManagedIdentityClientID string
	provisionShardID                                     *api.InternalID
	maestroConsumerName                                  string
	maestroRESTAPIURL                                    string
	maestroGRPCTarget                                    string
	kubeApplierCosmosContainerName                       string
}

type RegisterOptions struct {
	*registerOptions
}

func (o *ValidatedRegisterOptions) Complete(ctx context.Context) (*RegisterOptions, error) {
	clientOpts := azsdk.NewClientOptions(azsdk.ComponentFleet)
	clientOpts.Cloud = o.cloudConfiguration

	dbClient, err := database.NewCosmosDatabaseClient(o.CosmosURL, o.CosmosName, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create CosmosDB client: %w", err)
	}

	fleetDBClient, err := database.NewFleetDBClient(dbClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create fleet DB client: %w", err)
	}

	return &RegisterOptions{
		registerOptions: &registerOptions{
			fleetDBClient:                              fleetDBClient,
			stampIdentifier:                            o.StampIdentifier,
			stampResourceID:                            o.stampResourceID,
			managementClusterResourceID:                o.managementClusterResourceID,
			autoApprove:                                o.AutoApprove,
			schedulingPolicy:                           o.schedulingPolicy,
			aksResourceID:                              o.aksResourceID,
			publicDNSZoneResourceID:                    o.publicDNSZoneResourceID,
			hostedClustersSecretsKeyVaultURL:           o.HostedClustersSecretsKeyVaultURL,
			hostedClustersManagedIdentitiesKeyVaultURL: o.HostedClustersManagedIdentitiesKeyVaultURL,
			hostedClustersSecretsKeyVaultManagedIdentityClientID: o.HostedClustersSecretsKeyVaultManagedIdentityClientID,
			provisionShardID:               o.provisionShardID,
			maestroConsumerName:            o.MaestroConsumerName,
			maestroRESTAPIURL:              o.MaestroRESTAPIURL,
			maestroGRPCTarget:              o.MaestroGRPCTarget,
			kubeApplierCosmosContainerName: o.KubeApplierCosmosContainerName,
		},
	}, nil
}
