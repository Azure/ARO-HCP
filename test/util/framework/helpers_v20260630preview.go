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

package framework

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// ---------------------------------------------------------------------------
// Types (from deployment_params.go)
// ---------------------------------------------------------------------------

type ClusterParams20260630 struct {
	OpenshiftVersionId            string
	ClusterName                   string
	ManagedResourceGroupName      string
	NsgResourceID                 string
	NsgName                       string
	SubnetResourceID              string
	SubnetName                    string
	VnetName                      string
	UserAssignedIdentitiesProfile *hcpsdk20260630preview.UserAssignedIdentitiesProfile
	Identity                      *hcpsdk20260630preview.ManagedServiceIdentity
	KeyVaultName                  string
	EtcdEncryptionKeyName         string
	EtcdEncryptionKeyVersion      string
	EncryptionKeyManagementMode   string
	EncryptionType                string
	VnetIntegrationSubnetID       string
	KeyVaultVisibility            string
	IngressType                   string
	Network                       NetworkConfig
	APIVisibility                 string
	ImageRegistryState            string
	ChannelGroup                  string
	AuthorizedCIDRs               []*string
	Autoscaling                   *hcpsdk20260630preview.ClusterAutoscalingProfile
	CryptoRestrictions            *hcpsdk20260630preview.CryptoRestrictions
	Tags                          map[string]*string
}

type NodePoolParams20260630 struct {
	OpenshiftVersionId     string
	ClusterName            string
	NodePoolName           string
	Replicas               int32
	VMSize                 string
	OSDiskSizeGiB          int32
	DiskType               hcpsdk20260630preview.OsDiskType
	DiskStorageAccountType string
	ChannelGroup           string
	// NodeDrainTimeoutMinutes: how long (in minutes) to respect Pod Disruption Budgets when draining
	// nodes in this pool (e.g. upgrades, scale-in). Valid: 0 to 10080. 0 = no time limit for that phase.
	// When omitted from the create payload or nil here, the cluster-configured global nodeDrainTimeoutMinutes kicks in.
	NodeDrainTimeoutMinutes *int32
	// AutoScaling enables nodepool autoscaling. When set, Replicas is ignored.
	AutoScaling      *NodePoolAutoScalingParams
	AvailabilityZone string
	AutoRepair       bool
	Tags             map[string]*string
}

// ---------------------------------------------------------------------------
// Default parameter constructors (from deployment_params.go)
// ---------------------------------------------------------------------------

func NewDefaultClusterParams20260630() ClusterParams20260630 {
	params := ClusterParams20260630{
		OpenshiftVersionId: DefaultOpenshiftControlPlaneVersionId(),
		Network: NetworkConfig{
			NetworkType: "OVNKubernetes",
			PodCIDR:     DefaultPodCIDR,
			ServiceCIDR: DefaultServiceCIDR,
			MachineCIDR: "10.0.0.0/16",
			HostPrefix:  23,
		},
		EncryptionKeyManagementMode: "CustomerManaged",
		EncryptionType:              "KMS",
		KeyVaultVisibility:          "Public",
		IngressType:                 "Public",
		APIVisibility:               "Public",
		ImageRegistryState:          "Enabled",
		ChannelGroup:                DefaultOpenshiftChannelGroup(),
		// NOTE: The E2E subscription must have the ExperimentalReleaseFeatures AFEC
		// registered for these tags to be honored.
		Tags: map[string]*string{
			metadataapi.TagClusterSizeOverride:        to.Ptr(string(coreapi.MinimalControlPlanePodSizing)),
			metadataapi.TagClusterMaxCreationDuration: to.Ptr((ClusterCreationTimeout - time.Minute).String()),
			metadataapi.TagClusterMaxDeletionDuration: to.Ptr((HCPClusterDeletionTimeout - time.Minute).String()),
		},
	}
	applyCPOImageOverride(params.Tags)
	return params
}

func NewDefaultNodePoolParams20260630() NodePoolParams20260630 {
	return NodePoolParams20260630{
		OpenshiftVersionId: DefaultOpenshiftNodePoolVersionId(),
		Replicas:           int32(2),
		// VMSize is intentionally left empty: CreateNodePoolFromParam20260630
		// resolves it via the restriction-aware DefaultWorkerVMSizeSelector at
		// create time. Set it explicitly to pin a specific size.
		VMSize:                 "",
		OSDiskSizeGiB:          int32(64),
		DiskStorageAccountType: DefaultDiskStorageAccountType,
		ChannelGroup:           DefaultOpenshiftNodePoolChannelGroup(),
		// NOTE: The E2E subscription must have the ExperimentalReleaseFeatures AFEC
		// registered for these tags to be honored.
		Tags: map[string]*string{
			metadataapi.TagNodePoolMaxCreationDuration: to.Ptr((NodePoolCreationTimeout - time.Minute).String()),
		},
	}
}

// ---------------------------------------------------------------------------
// Conversion helpers (from deployment_params.go)
// ---------------------------------------------------------------------------

func ConvertToUserAssignedIdentitiesProfile20260630(value interface{}) (*hcpsdk20260630preview.UserAssignedIdentitiesProfile, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal UserAssignedIdentitiesValue: %w", err)
	}
	var uamis hcpsdk20260630preview.UserAssignedIdentitiesProfile
	if err := json.Unmarshal(b, &uamis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal UserAssignedIdentitiesValue: %w", err)
	}
	return &uamis, nil
}

// ClearUserAssignedIdentityValues20260630 resets every value in a
// ManagedServiceIdentity's UserAssignedIdentities map to an empty struct,
// preserving only the map keys (identity resource IDs).
//
// ARM requires that on a PUT of an existing resource, UserAssignedIdentities
// map values for identities that should be kept unchanged are sent back as
// empty objects ({}); the client/PrincipalID values a prior GET populated
// must not be echoed back. Callers that Get a cluster, mutate an unrelated
// field, and then BeginCreateOrUpdate the full object must call this first
// or ARM rejects the request with error code InvalidIdentityValues.
func ClearUserAssignedIdentityValues20260630(identity *hcpsdk20260630preview.ManagedServiceIdentity) {
	if identity == nil {
		return
	}
	for id := range identity.UserAssignedIdentities {
		identity.UserAssignedIdentities[id] = &hcpsdk20260630preview.UserAssignedIdentity{}
	}
}

func ConvertToManagedServiceIdentity20260630(value interface{}) (*hcpsdk20260630preview.ManagedServiceIdentity, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IdentityValue: %w", err)
	}
	var msi hcpsdk20260630preview.ManagedServiceIdentity
	if err := json.Unmarshal(b, &msi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal IdentityValue: %w", err)
	}
	return &msi, nil
}

// ---------------------------------------------------------------------------
// Populate helpers (from deployment_params.go)
// ---------------------------------------------------------------------------

func PopulateClusterParamsFromCustomerInfraDeployment20260630(
	params ClusterParams20260630,
	customerInfraDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20260630, error) {
	if customerInfraDeploymentResult == nil {
		return params, fmt.Errorf("customerInfraDeploymentResult cannot be nil")
	}

	keyVaultName, err := GetOutputValueString(customerInfraDeploymentResult, "keyVaultName")
	if err != nil {
		return params, fmt.Errorf("failed to get keyVaultName from customer infra deployment: %w", err)
	}
	etcdEncryptionKeyVersion, err := GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
	if err != nil {
		return params, fmt.Errorf("failed to get etcdEncryptionKeyVersion from customer infra deployment: %w", err)
	}
	etcdEncryptionKeyName, err := GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyName")
	if err != nil {
		return params, fmt.Errorf("failed to get etcdEncryptionKeyName from customer infra deployment: %w", err)
	}
	nsgResourceID, err := GetOutputValueString(customerInfraDeploymentResult, "nsgID")
	if err != nil {
		return params, fmt.Errorf("failed to get nsgID from customer infra deployment: %w", err)
	}
	subnetResourceID, err := GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetID")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetSubnetID from customer infra deployment: %w", err)
	}
	vnetIntegrationSubnetID, err := GetOutputValueString(customerInfraDeploymentResult, "vnetIntegrationSubnetID")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetIntegrationSubnetID from customer infra deployment: %w", err)
	}
	vnetName, err := GetOutputValueString(customerInfraDeploymentResult, "vnetName")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetName from customer infra deployment: %w", err)
	}
	nsgName, err := GetOutputValueString(customerInfraDeploymentResult, "nsgName")
	if err != nil {
		return params, fmt.Errorf("failed to get nsgName from customer infra deployment: %w", err)
	}
	subnetName, err := GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetName")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetSubnetName from customer infra deployment: %w", err)
	}
	params.KeyVaultName = keyVaultName
	params.EtcdEncryptionKeyVersion = etcdEncryptionKeyVersion
	params.EtcdEncryptionKeyName = etcdEncryptionKeyName
	params.NsgResourceID = nsgResourceID
	params.SubnetResourceID = subnetResourceID
	params.VnetIntegrationSubnetID = vnetIntegrationSubnetID
	params.VnetName = vnetName
	params.NsgName = nsgName
	params.SubnetName = subnetName
	return params, nil
}

func PopulateClusterParamsFromManagedIdentitiesDeployment20260630(
	params ClusterParams20260630,
	managedIdentitiesDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20260630, error) {
	if managedIdentitiesDeploymentResult == nil {
		return params, fmt.Errorf("managedIdentitiesDeploymentResult cannot be nil")
	}

	userAssignedIdentities, err := GetOutputValue(managedIdentitiesDeploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		return params, fmt.Errorf("failed to get userAssignedIdentitiesValue from managed identity deployment: %w", err)
	}
	userAssignedIdentitiesProfile, err := ConvertToUserAssignedIdentitiesProfile20260630(userAssignedIdentities)
	if err != nil {
		return params, fmt.Errorf("failed to convert userAssignedIdentitiesValue: %w", err)
	}

	identityValue, err := GetOutputValue(managedIdentitiesDeploymentResult, "identityValue")
	if err != nil {
		return params, fmt.Errorf("failed to get identityValue from managed identity deployment: %w", err)
	}
	identityProfile, err := ConvertToManagedServiceIdentity20260630(identityValue)
	if err != nil {
		return params, fmt.Errorf("failed to convert identityValue: %w", err)
	}

	params.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
	params.Identity = identityProfile

	return params, nil
}

// ---------------------------------------------------------------------------
// Customer resource deployment (from deployment_params.go)
// ---------------------------------------------------------------------------

func (tc *perItOrDescribeTestContext) CreateClusterCustomerResources20260630(ctx context.Context,
	resourceGroup *armresources.ResourceGroup,
	clusterParams ClusterParams20260630,
	infraParameters map[string]interface{},
	artifactsFS embed.FS,
	rbacScope RBACScope,
) (ClusterParams20260630, error) {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy customer resources in resource group %s", *resourceGroup.Name), startTime, finishTime)
	}()

	// Generate unique deployment names by combining cluster name with random suffix
	randomSuffix := rand.String(6)
	customerInfraDeploymentName := fmt.Sprintf("customer-infra-%s-%s", clusterParams.ClusterName, randomSuffix)
	managedIdentitiesDeploymentName := fmt.Sprintf("mi-%s-%s", clusterParams.ClusterName, randomSuffix)

	// ensure customer-infra resource names are unique per cluster
	infraParameters["clusterName"] = clusterParams.ClusterName

	customerInfraDeploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
		WithTemplateFromFS(artifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
		WithDeploymentName(customerInfraDeploymentName),
		WithScope(BicepDeploymentScopeResourceGroup),
		WithClusterResourceGroup(*resourceGroup.Name),
		WithParameters(infraParameters),
		WithTimeout(45*time.Minute),
	)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to create customer-infra: %w", err)
	}
	clusterParams, err = PopulateClusterParamsFromCustomerInfraDeployment20260630(clusterParams, customerInfraDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from customer-infra: %w", err)
	}

	managedIdentityDeploymentResult, err := tc.DeployManagedIdentities(ctx,
		clusterParams.ClusterName,
		rbacScope,
		WithTemplateFromFS(artifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
		WithDeploymentName(managedIdentitiesDeploymentName),
		WithClusterResourceGroup(*resourceGroup.Name),
		WithParameters(map[string]interface{}{
			"nsgName":      clusterParams.NsgName,
			"vnetName":     clusterParams.VnetName,
			"subnetName":   clusterParams.SubnetName,
			"keyVaultName": clusterParams.KeyVaultName,
		}),
	)

	if err != nil {
		return clusterParams, fmt.Errorf("failed to create managed identities: %w", err)
	}
	clusterParams, err = PopulateClusterParamsFromManagedIdentitiesDeployment20260630(clusterParams, managedIdentityDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from managed identities: %w", err)
	}
	return clusterParams, nil
}

// ---------------------------------------------------------------------------
// Cluster create / node pool create wrappers (from deployment_helper.go)
// ---------------------------------------------------------------------------

func (tc *perItOrDescribeTestContext) CreateHCPClusterFromParam20260630(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	parameters ClusterParams20260630,
	imageDigestMirrors []*hcpsdk20260630preview.ImageDigestMirror,
	timeout time.Duration,
) error {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20260630FromParam for cluster %s in resource group %s", timeout.Minutes(), parameters.ClusterName, resourceGroupName))
		defer cancel()
	}
	clusterName := parameters.ClusterName

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy HCP cluster %s/%s (v20260630preview)", resourceGroupName, clusterName), startTime, finishTime)
	}()

	cluster, err := BuildHCPClusterFromParams20260630(parameters, tc.Location(), imageDigestMirrors)
	if err != nil {
		return fmt.Errorf("failed to build HCP cluster %s: %w", clusterName, err)
	}

	if _, err := CreateHCPClusterAndWait20260630(
		ctx,
		logger,
		tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		resourceGroupName,
		clusterName,
		cluster,
		timeout,
	); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed to create HCP cluster %s, caused by: %w, error: %w", clusterName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed to create HCP cluster %s: %w", clusterName, err)
	}
	return nil
}

func (tc *perItOrDescribeTestContext) CreateNodePoolFromParam20260630(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	managedResourceGroupName string,
	hcpClusterName string,
	parameters NodePoolParams20260630,
	timeout time.Duration,
) error {
	nodePoolCtx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolFromParam for node pool %s in resource group %s", timeout.Minutes(), parameters.NodePoolName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy node pool %s", parameters.NodePoolName), startTime, finishTime)
	}()

	nodePoolName := parameters.NodePoolName
	if nodePoolName == "" {
		return fmt.Errorf("nodePoolName parameter not found or empty")
	}

	if parameters.VMSize == "" {
		vmSize, err := tc.SelectVMSize(nodePoolCtx, DefaultWorkerVMSizeSelector())
		if err != nil {
			return fmt.Errorf("failed to resolve default VM size for node pool %s (check VM SKU restrictions/quota for the test subscription in %s): %w", nodePoolName, tc.Location(), err)
		}
		parameters.VMSize = vmSize
	}

	nodePool := BuildNodePoolFromParams20260630(parameters, tc.Location())

	if _, err := CreateNodePoolAndWait20260630(
		nodePoolCtx,
		tc.Get20260630ClientFactoryOrDie(nodePoolCtx).NewNodePoolsClient(),
		resourceGroupName,
		hcpClusterName,
		nodePoolName,
		nodePool,
		timeout,
	); err != nil {
		// a separate context for console log download with its own timeout,
		// to make sure logs are fetched even when the node pool deployment
		// context is cancelled due to timeout
		downloadTimeout := 5 * time.Minute
		downloadCtx, downloadCancel := context.WithTimeoutCause(
			ctx,
			downloadTimeout,
			fmt.Errorf("timeout '%f' minutes exceeded during DownloadAllVirtualMachineConsoleLogs for VMs in managed resource group %s", downloadTimeout.Minutes(), managedResourceGroupName))
		defer downloadCancel()
		computeFactory, clientErr := tc.GetARMComputeClientFactory(downloadCtx)
		if clientErr == nil {
			consoleLogErr := DownloadAllVirtualMachineConsoleLogs(
				downloadCtx,
				computeFactory,
				managedResourceGroupName,
				tc.LogDirPath)
			if consoleLogErr != nil {
				logger.Error(consoleLogErr, "failed to download VM console logs")
			}
		} else {
			logger.Error(clientErr, "failed to get ARM compute client to download VM console logs")
		}

		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed to create NodePool %s, caused by: %w, error: %w", nodePoolName, context.Cause(nodePoolCtx), err)
		}
		return fmt.Errorf("failed to create NodePool %s: %w", nodePoolName, err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// HCP cluster build / create / update / get (from hcp_helper.go)
// ---------------------------------------------------------------------------

func BuildHCPClusterFromParams20260630(
	parameters ClusterParams20260630,
	location string,
	imageDigestMirrors []*hcpsdk20260630preview.ImageDigestMirror,
) (hcpsdk20260630preview.HcpOpenShiftCluster, error) {
	// Convert identity types from old SDK to new SDK via JSON round-trip
	var identity *hcpsdk20260630preview.ManagedServiceIdentity
	if parameters.Identity != nil {
		var err error
		identity, err = convertViaJSON[hcpsdk20260630preview.ManagedServiceIdentity](parameters.Identity)
		if err != nil {
			return hcpsdk20260630preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert Identity: %w", err)
		}
	}

	var uamis *hcpsdk20260630preview.UserAssignedIdentitiesProfile
	if parameters.UserAssignedIdentitiesProfile != nil {
		var err error
		uamis, err = convertViaJSON[hcpsdk20260630preview.UserAssignedIdentitiesProfile](parameters.UserAssignedIdentitiesProfile)
		if err != nil {
			return hcpsdk20260630preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert UserAssignedIdentitiesProfile: %w", err)
		}
	}

	return hcpsdk20260630preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: identity,
		Tags:     parameters.Tags,
		Properties: &hcpsdk20260630preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20260630preview.VersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Platform: &hcpsdk20260630preview.PlatformProfile{
				ManagedResourceGroup:    to.Ptr(parameters.ManagedResourceGroupName),
				NetworkSecurityGroupID:  to.Ptr(parameters.NsgResourceID),
				SubnetID:                to.Ptr(parameters.SubnetResourceID),
				VnetIntegrationSubnetID: to.Ptr(parameters.VnetIntegrationSubnetID),
				OperatorsAuthentication: &hcpsdk20260630preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: uamis,
				},
			},
			Network: &hcpsdk20260630preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20260630preview.NetworkType(parameters.Network.NetworkType)),
				PodCIDR:     to.Ptr(parameters.Network.PodCIDR),
				ServiceCIDR: to.Ptr(parameters.Network.ServiceCIDR),
				MachineCIDR: to.Ptr(parameters.Network.MachineCIDR),
				HostPrefix:  to.Ptr(parameters.Network.HostPrefix),
			},
			API: &hcpsdk20260630preview.APIProfile{
				Visibility:      to.Ptr(hcpsdk20260630preview.Visibility(parameters.APIVisibility)),
				AuthorizedCIDRs: parameters.AuthorizedCIDRs,
			},
			Ingress: &hcpsdk20260630preview.IngressProfile{
				Type: to.Ptr(hcpsdk20260630preview.IngressType(parameters.IngressType)),
			},
			ClusterImageRegistry: &hcpsdk20260630preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20260630preview.ClusterImageRegistryState(parameters.ImageRegistryState)),
			},
			CryptoRestrictions: parameters.CryptoRestrictions,
			Etcd: &hcpsdk20260630preview.EtcdProfile{
				DataEncryption: &hcpsdk20260630preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20260630preview.EtcdDataEncryptionKeyManagementModeType(parameters.EncryptionKeyManagementMode)),
					CustomerManaged: &hcpsdk20260630preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20260630preview.CustomerManagedEncryptionType(parameters.EncryptionType)),
						Kms: &hcpsdk20260630preview.KmsEncryptionProfile{
							VaultName:  to.Ptr(parameters.KeyVaultName),
							Visibility: to.Ptr(hcpsdk20260630preview.KeyVaultVisibility(parameters.KeyVaultVisibility)),
							ActiveKey: &hcpsdk20260630preview.KmsKey{
								Name:    to.Ptr(parameters.EtcdEncryptionKeyName),
								Version: to.Ptr(parameters.EtcdEncryptionKeyVersion),
							},
						},
					},
				},
			},
			ImageDigestMirrors: imageDigestMirrors,
		},
	}, nil
}

// CreateHCPClusterAndWait20260630 creates an HCP cluster using the v20260630preview API and waits for completion.
func CreateHCPClusterAndWait20260630(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20260630preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20260630preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20260630preview.HcpOpenShiftCluster, error) {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20260630AndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
		defer cancel()
	}

	logger.Info("Starting HCP cluster creation (v20260630preview)", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName)
	poller, err := hcpClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, cluster, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting cluster creation %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}

	if timeout > 0*time.Second {
		operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating: %w", hcpClusterName, resourceGroupName, err)
		}
		switch m := any(operationResult).(type) {
		case hcpsdk20260630preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
			return &m.HcpOpenShiftCluster, nil
		default:
			ginkgo.GinkgoLogr.Info("unexpected operation result", "type", fmt.Sprintf("%T", m), "content", spew.Sdump(m))
			return nil, fmt.Errorf("unknown type %T", m)
		}
	} else {
		_, err := poller.Poll(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed checking for deployment %q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed checking for deployment %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
		}
		return nil, nil
	}
}

// UpdateHCPCluster20260630 updates an HCP cluster using the v20260630preview SDK and waits for the operation to complete.
// Transient 500 and 409 errors are retried automatically with exponential backoff.
func UpdateHCPCluster20260630(
	ctx context.Context,
	hcpClient *hcpsdk20260630preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20260630preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (*hcpsdk20260630preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateHCPCluster20260630 for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var hcpOpenShiftCluster *hcpsdk20260630preview.HcpOpenShiftCluster
	var lastTransientErr error
	attempt := 0

	err := wait.ExponentialBackoffWithContext(ctx, stateConflictBackoff, func(ctx context.Context) (bool, error) {
		attempt++
		if attempt > 1 {
			ginkgo.GinkgoLogr.Info("Retrying cluster update",
				"cluster", hcpClusterName,
				"attempt", attempt)
		}

		poller, err := hcpClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, update, nil)
		if err != nil {
			if isTransientUpdateError(err) {
				lastTransientErr = err
				return false, nil
			}
			return false, err
		}

		operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			if isTransientUpdateError(err) {
				lastTransientErr = err
				return false, nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return false, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return false, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating: %w", hcpClusterName, resourceGroupName, err)
		}

		// The v20260630 variant does not call checkOperationResult because
		// the v20260630 SDK does not yet have a matching Get response type
		// to compare against. The v20240610 variant above does this check.
		hcpOpenShiftCluster = &operationResult.HcpOpenShiftCluster
		return true, nil
	})

	if wait.Interrupted(err) && lastTransientErr != nil {
		return hcpOpenShiftCluster, fmt.Errorf("update interrupted for hcpCluster=%q in resourcegroup=%q after %d attempts, last transient error: %w, interrupt cause: %w", hcpClusterName, resourceGroupName, attempt, lastTransientErr, err)
	}

	return hcpOpenShiftCluster, err
}

// ---------------------------------------------------------------------------
// Node pool build / create / get (from hcp_helper.go)
// ---------------------------------------------------------------------------

func BuildNodePoolFromParams20260630(
	parameters NodePoolParams20260630,
	location string,
) hcpsdk20260630preview.NodePool {

	nodePool := hcpsdk20260630preview.NodePool{
		Location: to.Ptr(location),
		Tags:     parameters.Tags,
		Properties: &hcpsdk20260630preview.NodePoolProperties{
			Version: &hcpsdk20260630preview.NodePoolVersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			NodeDrainTimeoutMinutes: parameters.NodeDrainTimeoutMinutes,
			Platform: &hcpsdk20260630preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(parameters.VMSize),
				OSDisk: &hcpsdk20260630preview.OsDiskProfile{
					SizeGiB:                to.Ptr(parameters.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20260630preview.DiskStorageAccountType(parameters.DiskStorageAccountType)),
					DiskType:               to.Ptr(parameters.DiskType),
				},
				AvailabilityZone: to.Ptr(parameters.AvailabilityZone),
			},
			AutoRepair: to.Ptr(parameters.AutoRepair),
		},
	}

	if parameters.AutoScaling != nil {
		nodePool.Properties.AutoScaling = &hcpsdk20260630preview.NodePoolAutoScaling{
			Min: to.Ptr(parameters.AutoScaling.Min),
			Max: to.Ptr(parameters.AutoScaling.Max),
		}
	} else {
		nodePool.Properties.Replicas = to.Ptr(parameters.Replicas)
	}

	return nodePool
}

// CreateNodePoolAndWait20260630 creates a nodepool using the v20260630preview API and waits for completion.
func CreateNodePoolAndWait20260630(
	ctx context.Context,
	nodePoolsClient *hcpsdk20260630preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20260630preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20260630preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolAndWait20260630 for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()
	poller, err := nodePoolsClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, nodePoolName, nodePool, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting nodepool creation %q for cluster %q in resourcegroup=%q: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for nodepool=%q for cluster %q in resourcegroup=%q to finish creating: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	// Verify the LRO result body matches a fresh GET, per ARM LRO contract.
	expect, err := GetNodePool20260630(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed to get nodepool, caused by: %w, error: %w", context.Cause(ctx), err)
		}
		return nil, err
	}
	if err := checkOperationResult(expect, &operationResult.NodePool); err != nil {
		return nil, err
	}

	return &operationResult.NodePool, nil
}

// GetNodePool20260630 retrieves a nodepool using the v20260630preview API.
func GetNodePool20260630(
	ctx context.Context,
	nodePoolsClient *hcpsdk20260630preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (*hcpsdk20260630preview.NodePool, error) {
	resp, err := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		return nil, err
	}
	return &resp.NodePool, nil
}

// ---------------------------------------------------------------------------
// Client factory methods (from per_test_framework.go)
// ---------------------------------------------------------------------------

func (tc *perItOrDescribeTestContext) Get20260630ClientFactory(ctx context.Context) (*hcpsdk20260630preview.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20260630 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20260630, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20260630ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) Get20260630ClientFactoryOrDie(ctx context.Context) *hcpsdk20260630preview.ClientFactory {
	return Must(tc.Get20260630ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) get20260630ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20260630preview.ClientFactory, error) {
	if tc.clientFactory20260630 != nil {
		return tc.clientFactory20260630, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpsdk20260630preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20260630 = clientFactory

	return tc.clientFactory20260630, nil
}
