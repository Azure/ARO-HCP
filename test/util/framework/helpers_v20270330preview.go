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
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"

	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20270330preview "github.com/Azure/ARO-HCP/test/sdk/v20270330preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// --- Types from deployment_params.go ---

type ClusterParams20270330 struct {
	OpenshiftVersionId               string
	ClusterName                      string
	ManagedResourceGroupName         string
	NsgResourceID                    string
	NsgName                          string
	SubnetResourceID                 string
	SubnetName                       string
	VnetName                         string
	UserAssignedIdentitiesProfile    *hcpsdk20270330preview.UserAssignedIdentitiesProfile
	Identity                         *hcpsdk20270330preview.ManagedServiceIdentity
	KeyVaultName                     string
	EtcdEncryptionKeyName            string
	EtcdEncryptionKeyVersion         string
	EncryptionKeyManagementMode      string
	EncryptionType                   string
	VnetIntegrationSubnetID          string
	DisableSwift                     bool // Test-only networking choice, independent of visibility.
	KeyVaultVisibility               string
	IngressType                      string
	Network                          NetworkConfig
	APIVisibility                    string
	ImageRegistryState               string
	ChannelGroup                     string
	AuthorizedCIDRs                  []*string
	Autoscaling                      *hcpsdk20270330preview.ClusterAutoscalingProfile
	CryptoRestrictions               *hcpsdk20270330preview.CryptoRestrictions
	ContainerRegistryManagedIdentity *string
	Tags                             map[string]*string
	// AutoNodeMode sets the typed properties.autoNode.mode field introduced in
	// this API version. Nil means the field is omitted from the request
	// entirely (the legacy TagClusterAutoNode tag, if set in Tags, still
	// works — this version's Normalize folds either into the same tag before
	// admission runs).
	AutoNodeMode *hcpsdk20270330preview.AutoNodeMode
}

type NodePoolParams20270330 struct {
	OpenshiftVersionId     string
	ClusterName            string
	NodePoolName           string
	Replicas               int32
	VMSize                 string
	OSDiskSizeGiB          int32
	DiskType               hcpsdk20270330preview.OsDiskType
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

// --- Functions from deployment_params.go ---

func NewDefaultClusterParams20270330() ClusterParams20270330 {
	params := ClusterParams20270330{
		DisableSwift:       true,
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

func NewDefaultNodePoolParams20270330() NodePoolParams20270330 {
	return NodePoolParams20270330{
		OpenshiftVersionId: DefaultOpenshiftNodePoolVersionId(),
		Replicas:           int32(2),
		// VMSize is intentionally left empty: CreateNodePoolFromParam20270330
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

func ConvertToUserAssignedIdentitiesProfile20270330(value interface{}) (*hcpsdk20270330preview.UserAssignedIdentitiesProfile, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal UserAssignedIdentitiesValue: %w", err)
	}
	var uamis hcpsdk20270330preview.UserAssignedIdentitiesProfile
	if err := json.Unmarshal(b, &uamis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal UserAssignedIdentitiesValue: %w", err)
	}
	return &uamis, nil
}

func ConvertToManagedServiceIdentity20270330(value interface{}) (*hcpsdk20270330preview.ManagedServiceIdentity, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IdentityValue: %w", err)
	}
	var msi hcpsdk20270330preview.ManagedServiceIdentity
	if err := json.Unmarshal(b, &msi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal IdentityValue: %w", err)
	}
	return &msi, nil
}

func PopulateClusterParamsFromCustomerInfraDeployment20270330(
	params ClusterParams20270330,
	customerInfraDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20270330, error) {
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

func PopulateClusterParamsFromManagedIdentitiesDeployment20270330(
	params ClusterParams20270330,
	managedIdentitiesDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20270330, error) {
	if managedIdentitiesDeploymentResult == nil {
		return params, fmt.Errorf("managedIdentitiesDeploymentResult cannot be nil")
	}

	userAssignedIdentities, err := GetOutputValue(managedIdentitiesDeploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		return params, fmt.Errorf("failed to get userAssignedIdentitiesValue from managed identity deployment: %w", err)
	}
	userAssignedIdentitiesProfile, err := ConvertToUserAssignedIdentitiesProfile20270330(userAssignedIdentities)
	if err != nil {
		return params, fmt.Errorf("failed to convert userAssignedIdentitiesValue: %w", err)
	}

	identityValue, err := GetOutputValue(managedIdentitiesDeploymentResult, "identityValue")
	if err != nil {
		return params, fmt.Errorf("failed to get identityValue from managed identity deployment: %w", err)
	}
	identityProfile, err := ConvertToManagedServiceIdentity20270330(identityValue)
	if err != nil {
		return params, fmt.Errorf("failed to convert identityValue: %w", err)
	}

	params.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
	params.Identity = identityProfile

	return params, nil
}

// CreateClusterCustomerResources20270330 keeps the enableAutoNode parameter
// that CreateClusterCustomerResources20261001 (the version this file was
// copied from) dropped from its DeployManagedIdentities call — this version
// is used by the AutoNode e2e test, which needs the "autonode" data-plane
// identity actually provisioned.
func (tc *perItOrDescribeTestContext) CreateClusterCustomerResources20270330(ctx context.Context,
	resourceGroup *armresources.ResourceGroup,
	clusterParams ClusterParams20270330,
	infraParameters map[string]interface{},
	artifactsFS embed.FS,
	rbacScope RBACScope,
	enableAutoNode bool,
) (ClusterParams20270330, error) {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy customer resources in resource group %s", *resourceGroup.Name), startTime, finishTime)
	}()

	// Generate unique deployment names by combining cluster name with random suffix
	randomSuffix := utilrand.String(6)
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
	clusterParams, err = PopulateClusterParamsFromCustomerInfraDeployment20270330(clusterParams, customerInfraDeploymentResult)
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
			"nsgName":        clusterParams.NsgName,
			"vnetName":       clusterParams.VnetName,
			"subnetName":     clusterParams.SubnetName,
			"keyVaultName":   clusterParams.KeyVaultName,
			"enableAutoNode": enableAutoNode,
		}),
	)

	if err != nil {
		return clusterParams, fmt.Errorf("failed to create managed identities: %w", err)
	}
	clusterParams, err = PopulateClusterParamsFromManagedIdentitiesDeployment20270330(clusterParams, managedIdentityDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from managed identities: %w", err)
	}
	return clusterParams, nil
}

// --- Functions from deployment_helper.go ---

func (tc *perItOrDescribeTestContext) CreateHCPClusterFromParam20270330(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	parameters ClusterParams20270330,
	imageDigestMirrors []*hcpsdk20270330preview.ImageDigestMirror,
	timeout time.Duration,
) error {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20270330FromParam for cluster %s in resource group %s", timeout.Minutes(), parameters.ClusterName, resourceGroupName))
		defer cancel()
	}
	clusterName := parameters.ClusterName

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy HCP cluster %s/%s (v20270330preview)", resourceGroupName, clusterName), startTime, finishTime)
	}()

	cluster, err := BuildHCPClusterFromParams20270330(parameters, tc.Location(), imageDigestMirrors)
	if err != nil {
		return fmt.Errorf("failed to build HCP cluster %s: %w", clusterName, err)
	}

	if _, err := CreateHCPClusterAndWait20270330(
		ctx,
		logger,
		tc.Get20270330ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
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

func (tc *perItOrDescribeTestContext) CreateNodePoolFromParam20270330(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	managedResourceGroupName string,
	hcpClusterName string,
	parameters NodePoolParams20270330,
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

	nodePool := BuildNodePoolFromParams20270330(parameters, tc.Location())

	if _, err := CreateNodePoolAndWait20270330(
		nodePoolCtx,
		tc.Get20270330ClientFactoryOrDie(nodePoolCtx).NewNodePoolsClient(),
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

// --- Functions from hcp_helper.go ---

func BuildHCPClusterFromParams20270330(
	parameters ClusterParams20270330,
	location string,
	imageDigestMirrors []*hcpsdk20270330preview.ImageDigestMirror,
) (hcpsdk20270330preview.HcpOpenShiftCluster, error) {
	// Convert identity types from old SDK to new SDK via JSON round-trip
	var identity *hcpsdk20270330preview.ManagedServiceIdentity
	if parameters.Identity != nil {
		var err error
		identity, err = convertViaJSON[hcpsdk20270330preview.ManagedServiceIdentity](parameters.Identity)
		if err != nil {
			return hcpsdk20270330preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert Identity: %w", err)
		}
	}

	var uamis *hcpsdk20270330preview.UserAssignedIdentitiesProfile
	if parameters.UserAssignedIdentitiesProfile != nil {
		var err error
		uamis, err = convertViaJSON[hcpsdk20270330preview.UserAssignedIdentitiesProfile](parameters.UserAssignedIdentitiesProfile)
		if err != nil {
			return hcpsdk20270330preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert UserAssignedIdentitiesProfile: %w", err)
		}
	}

	tags, vnetIntegrationSubnetID := buildClusterNetworking(parameters.DisableSwift, parameters.Tags, parameters.VnetIntegrationSubnetID)
	cluster := hcpsdk20270330preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: identity,
		Tags:     tags,
		Properties: &hcpsdk20270330preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20270330preview.VersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Platform: &hcpsdk20270330preview.PlatformProfile{
				ManagedResourceGroup:    to.Ptr(parameters.ManagedResourceGroupName),
				NetworkSecurityGroupID:  to.Ptr(parameters.NsgResourceID),
				SubnetID:                to.Ptr(parameters.SubnetResourceID),
				VnetIntegrationSubnetID: vnetIntegrationSubnetID,
				OperatorsAuthentication: &hcpsdk20270330preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: uamis,
				},
			},
			Network: &hcpsdk20270330preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20270330preview.NetworkType(parameters.Network.NetworkType)),
				PodCIDR:     to.Ptr(parameters.Network.PodCIDR),
				ServiceCIDR: to.Ptr(parameters.Network.ServiceCIDR),
				MachineCIDR: to.Ptr(parameters.Network.MachineCIDR),
				HostPrefix:  to.Ptr(parameters.Network.HostPrefix),
			},
			API: &hcpsdk20270330preview.APIProfile{
				Visibility:      to.Ptr(hcpsdk20270330preview.Visibility(parameters.APIVisibility)),
				AuthorizedCIDRs: parameters.AuthorizedCIDRs,
			},
			Ingress: &hcpsdk20270330preview.IngressProfile{
				Type: to.Ptr(hcpsdk20270330preview.IngressType(parameters.IngressType)),
			},
			ClusterImageRegistry: &hcpsdk20270330preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20270330preview.ClusterImageRegistryState(parameters.ImageRegistryState)),
			},
			CryptoRestrictions: parameters.CryptoRestrictions,
			Etcd: &hcpsdk20270330preview.EtcdProfile{
				DataEncryption: &hcpsdk20270330preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20270330preview.EtcdDataEncryptionKeyManagementModeType(parameters.EncryptionKeyManagementMode)),
					CustomerManaged: &hcpsdk20270330preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20270330preview.CustomerManagedEncryptionType(parameters.EncryptionType)),
						Kms: &hcpsdk20270330preview.KmsEncryptionProfile{
							VaultName:  to.Ptr(parameters.KeyVaultName),
							Visibility: to.Ptr(hcpsdk20270330preview.KeyVaultVisibility(parameters.KeyVaultVisibility)),
							ActiveKey: &hcpsdk20270330preview.KmsKey{
								Name:    to.Ptr(parameters.EtcdEncryptionKeyName),
								Version: to.Ptr(parameters.EtcdEncryptionKeyVersion),
							},
						},
					},
				},
			},
			ImageDigestMirrors: imageDigestMirrors,
		},
	}

	if parameters.ContainerRegistryManagedIdentity != nil {
		cluster.Properties.Platform.ContainerRegistry = &hcpsdk20270330preview.ContainerRegistryProfile{
			ManagedIdentity: parameters.ContainerRegistryManagedIdentity,
		}
	}
	if parameters.AutoNodeMode != nil {
		cluster.Properties.AutoNode = &hcpsdk20270330preview.AutoNodeProfile{
			Mode: parameters.AutoNodeMode,
		}
	}
	return cluster, nil
}

func CreateHCPClusterAndWait20270330(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20270330preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20270330preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20270330preview.HcpOpenShiftCluster, error) {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20270330AndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
		defer cancel()
	}

	logger.Info("Starting HCP cluster creation (v20270330preview)", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName)
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
		case hcpsdk20270330preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
			return &m.HcpOpenShiftCluster, nil
		default:
			fmt.Printf("unknown type %T: content=%v", m, spew.Sdump(m))
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

func UpdateHCPCluster20270330(
	ctx context.Context,
	hcpClient *hcpsdk20270330preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20270330preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20270330preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateHCPCluster20270330 for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var hcpOpenShiftCluster *hcpsdk20270330preview.HcpOpenShiftCluster
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

		hcpOpenShiftCluster = &operationResult.HcpOpenShiftCluster
		return true, nil
	})

	if wait.Interrupted(err) && lastTransientErr != nil {
		return hcpOpenShiftCluster, fmt.Errorf("update interrupted for hcpCluster=%q in resourcegroup=%q after %d attempts, last transient error: %w, interrupt cause: %w", hcpClusterName, resourceGroupName, attempt, lastTransientErr, err)
	}

	return hcpOpenShiftCluster, err
}

func BuildNodePoolFromParams20270330(
	parameters NodePoolParams20270330,
	location string,
) hcpsdk20270330preview.NodePool {

	nodePool := hcpsdk20270330preview.NodePool{
		Location: to.Ptr(location),
		Tags:     parameters.Tags,
		Properties: &hcpsdk20270330preview.NodePoolProperties{
			Version: &hcpsdk20270330preview.NodePoolVersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			NodeDrainTimeoutMinutes: parameters.NodeDrainTimeoutMinutes,
			Platform: &hcpsdk20270330preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(parameters.VMSize),
				OSDisk: &hcpsdk20270330preview.OsDiskProfile{
					SizeGiB:                to.Ptr(parameters.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20270330preview.DiskStorageAccountType(parameters.DiskStorageAccountType)),
					DiskType:               to.Ptr(parameters.DiskType),
				},
				AvailabilityZone: to.Ptr(parameters.AvailabilityZone),
			},
			AutoRepair: to.Ptr(parameters.AutoRepair),
		},
	}

	if parameters.AutoScaling != nil {
		nodePool.Properties.AutoScaling = &hcpsdk20270330preview.NodePoolAutoScaling{
			Min: to.Ptr(parameters.AutoScaling.Min),
			Max: to.Ptr(parameters.AutoScaling.Max),
		}
	} else {
		nodePool.Properties.Replicas = to.Ptr(parameters.Replicas)
	}

	return nodePool
}

func CreateNodePoolAndWait20270330(
	ctx context.Context,
	nodePoolsClient *hcpsdk20270330preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20270330preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20270330preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolAndWait20270330 for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
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

	expect, err := GetNodePool20270330(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
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

func GetNodePool20270330(
	ctx context.Context,
	nodePoolsClient *hcpsdk20270330preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (*hcpsdk20270330preview.NodePool, error) {
	resp, err := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		return nil, err
	}
	return &resp.NodePool, nil
}

// --- Functions from per_test_framework.go ---

func (tc *perItOrDescribeTestContext) Get20270330ClientFactory(ctx context.Context) (*hcpsdk20270330preview.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20270330 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20270330, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20270330ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) Get20270330ClientFactoryOrDie(ctx context.Context) *hcpsdk20270330preview.ClientFactory {
	return Must(tc.Get20270330ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) get20270330ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20270330preview.ClientFactory, error) {
	if tc.clientFactory20270330 != nil {
		return tc.clientFactory20270330, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpsdk20270330preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20270330 = clientFactory

	return tc.clientFactory20270330, nil
}

func (tc *perItOrDescribeTestContext) GetAdminRESTConfigForHCPCluster20270330(
	ctx context.Context,
	hcpClient *hcpsdk20270330preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) (*rest.Config, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during GetAdminRESTConfigForHCPCluster20270330 for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Collect admin credentials for cluster", startTime, finishTime)
	}()

	privKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	csrDER, err := x509.CreateCertificateRequest(cryptorand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   "system:customer-break-glass:system-admin",
			Organization: []string{"system:masters"},
		},
	}, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	adminCredentialRequestPoller, err := hcpClient.BeginRequestAdminCredential(
		ctx,
		resourceGroupName,
		hcpClusterName,
		hcpsdk20270330preview.HcpOpenShiftClusterAdminCredentialRequest{
			CertificateSigningRequest: to.Ptr(string(csrPEM)),
		},
		nil,
	)
	if err != nil {
		// Fall back to the old 0240610 mechanism during the transition period.
		fallbackFactory, fallbackErr := tc.Get20240610ClientFactory(ctx)
		if fallbackErr != nil {
			return nil, fmt.Errorf("1001 credential request failed: %w; fallback client factory error: %w", err, fallbackErr)
		}
		return tc.GetAdminRESTConfigForHCPCluster20240610(ctx, fallbackFactory.NewHcpOpenShiftClustersClient(), resourceGroupName, hcpClusterName, timeout)
	}

	operationResult, err := adminCredentialRequestPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish getting creds, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish getting creds: %w", hcpClusterName, resourceGroupName, err)
	}

	if operationResult.Kubeconfig == nil {
		return nil, fmt.Errorf("kubeconfig content is nil")
	}

	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	kubeconfigData, err := clientcmd.Load([]byte(*operationResult.Kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	for _, authInfo := range kubeconfigData.AuthInfos {
		authInfo.ClientKeyData = privKeyPEM
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*kubeconfigData, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}

	tc.contextLock.Lock()
	tc.hcpAdminConfigs[resourceGroupName+"/"+hcpClusterName] = restConfig
	tc.contextLock.Unlock()

	return restConfig, nil
}
