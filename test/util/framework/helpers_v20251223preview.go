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
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// ---------------------------------------------------------------------------
// Types (from deployment_params.go)
// ---------------------------------------------------------------------------

type ClusterParams20251223 struct {
	OpenshiftVersionId            string
	ClusterName                   string
	ManagedResourceGroupName      string
	NsgResourceID                 string
	NsgName                       string
	SubnetResourceID              string
	SubnetName                    string
	VnetName                      string
	UserAssignedIdentitiesProfile *hcpsdk20251223preview.UserAssignedIdentitiesProfile
	Identity                      *hcpsdk20251223preview.ManagedServiceIdentity
	KeyVaultName                  string
	EtcdEncryptionKeyName         string
	EtcdEncryptionKeyVersion      string
	EncryptionKeyManagementMode   string
	EncryptionType                string
	VnetIntegrationSubnetID       string
	KeyVaultVisibility            string
	Network                       NetworkConfig
	APIVisibility                 string
	ImageRegistryState            string
	ChannelGroup                  string
	AuthorizedCIDRs               []*string
	Autoscaling                   *hcpsdk20251223preview.ClusterAutoscalingProfile
	Tags                          map[string]*string
}

type NodePoolParams20251223 struct {
	OpenshiftVersionId     string
	ClusterName            string
	NodePoolName           string
	Replicas               int32
	VMSize                 string
	OSDiskSizeGiB          int32
	DiskType               hcpsdk20251223preview.OsDiskType
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
	// Labels are Kubernetes labels propagated to NodePool nodes.
	Labels []*hcpsdk20251223preview.Label
	// Taints are Kubernetes taints applied to NodePool nodes.
	Taints []*hcpsdk20251223preview.Taint
	Tags   map[string]*string
}

// ---------------------------------------------------------------------------
// Default params constructors (from deployment_params.go)
// ---------------------------------------------------------------------------

func NewDefaultClusterParams20251223() ClusterParams20251223 {
	params := ClusterParams20251223{
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

func NewDefaultNodePoolParams20251223() NodePoolParams20251223 {
	return NodePoolParams20251223{
		OpenshiftVersionId: DefaultOpenshiftNodePoolVersionId(),
		Replicas:           int32(2),
		// VMSize is intentionally left empty: CreateNodePoolFromParam20251223
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
// Conversion functions (from deployment_params.go)
// ---------------------------------------------------------------------------

func ConvertToUserAssignedIdentitiesProfile20251223(value interface{}) (*hcpsdk20251223preview.UserAssignedIdentitiesProfile, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal UserAssignedIdentitiesValue: %w", err)
	}
	var uamis hcpsdk20251223preview.UserAssignedIdentitiesProfile
	if err := json.Unmarshal(b, &uamis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal UserAssignedIdentitiesValue: %w", err)
	}
	return &uamis, nil
}

// ClearUserAssignedIdentityValues20251223 resets every value in a
// ManagedServiceIdentity's UserAssignedIdentities map to an empty struct,
// preserving only the map keys (identity resource IDs).
//
// ARM requires that on a PUT of an existing resource, UserAssignedIdentities
// map values for identities that should be kept unchanged are sent back as
// empty objects ({}); the client/PrincipalID values a prior GET populated
// must not be echoed back. Callers that Get a cluster, mutate an unrelated
// field, and then BeginCreateOrUpdate the full object must call this first
// or ARM rejects the request with error code InvalidIdentityValues.
func ClearUserAssignedIdentityValues20251223(identity *hcpsdk20251223preview.ManagedServiceIdentity) {
	if identity == nil {
		return
	}
	for id := range identity.UserAssignedIdentities {
		identity.UserAssignedIdentities[id] = &hcpsdk20251223preview.UserAssignedIdentity{}
	}
}

func ConvertToManagedServiceIdentity20251223(value interface{}) (*hcpsdk20251223preview.ManagedServiceIdentity, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IdentityValue: %w", err)
	}
	var msi hcpsdk20251223preview.ManagedServiceIdentity
	if err := json.Unmarshal(b, &msi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal IdentityValue: %w", err)
	}
	return &msi, nil
}

// BuildIdentityParamsFromNames constructs the UserAssignedIdentitiesProfile and
// ManagedServiceIdentity from identity names and resource group, without
// requiring a Bicep deployment. This produces the same structure as the outputs
// of non-msi-scoped-assignments.bicep.
func BuildIdentityParamsFromNames(
	subscriptionID string,
	msiResourceGroupName string,
	identities Identities,
) (*hcpsdk20251223preview.UserAssignedIdentitiesProfile, *hcpsdk20251223preview.ManagedServiceIdentity) {
	idFmt := "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s"
	id := func(name string) *string {
		return to.Ptr(fmt.Sprintf(idFmt, subscriptionID, msiResourceGroupName, name))
	}

	uamis := &hcpsdk20251223preview.UserAssignedIdentitiesProfile{
		ControlPlaneOperators: map[string]*string{
			"cluster-api-azure":        id(identities.ClusterApiAzureMiName),
			"control-plane":            id(identities.ControlPlaneMiName),
			"cloud-controller-manager": id(identities.CloudControllerManagerMiName),
			"ingress":                  id(identities.IngressMiName),
			"disk-csi-driver":          id(identities.DiskCsiDriverMiName),
			"file-csi-driver":          id(identities.FileCsiDriverMiName),
			"image-registry":           id(identities.ImageRegistryMiName),
			"cloud-network-config":     id(identities.CloudNetworkConfigMiName),
			"kms":                      id(identities.KmsMiName),
		},
		DataPlaneOperators: map[string]*string{
			"disk-csi-driver": id(identities.DpDiskCsiDriverMiName),
			"file-csi-driver": id(identities.DpFileCsiDriverMiName),
			"image-registry":  id(identities.DpImageRegistryMiName),
		},
		ServiceManagedIdentity: id(identities.ServiceManagedIdentityName),
	}

	azureAttachedIDs := []*string{
		id(identities.ServiceManagedIdentityName),
		id(identities.ClusterApiAzureMiName),
		id(identities.ControlPlaneMiName),
		id(identities.CloudControllerManagerMiName),
		id(identities.IngressMiName),
		id(identities.DiskCsiDriverMiName),
		id(identities.FileCsiDriverMiName),
		id(identities.ImageRegistryMiName),
		id(identities.CloudNetworkConfigMiName),
		id(identities.KmsMiName),
	}
	userAssigned := make(map[string]*hcpsdk20251223preview.UserAssignedIdentity, len(azureAttachedIDs))
	for _, armID := range azureAttachedIDs {
		userAssigned[*armID] = &hcpsdk20251223preview.UserAssignedIdentity{}
	}
	msi := &hcpsdk20251223preview.ManagedServiceIdentity{
		Type:                   to.Ptr(hcpsdk20251223preview.ManagedServiceIdentityTypeUserAssigned),
		UserAssignedIdentities: userAssigned,
	}

	return uamis, msi
}

// ---------------------------------------------------------------------------
// Populate functions (from deployment_params.go)
// ---------------------------------------------------------------------------

func PopulateClusterParamsFromCustomerInfraDeployment20251223(
	params ClusterParams20251223,
	customerInfraDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20251223, error) {
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

func PopulateClusterParamsFromManagedIdentitiesDeployment20251223(
	params ClusterParams20251223,
	managedIdentitiesDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20251223, error) {
	if managedIdentitiesDeploymentResult == nil {
		return params, fmt.Errorf("managedIdentitiesDeploymentResult cannot be nil")
	}

	userAssignedIdentities, err := GetOutputValue(managedIdentitiesDeploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		return params, fmt.Errorf("failed to get userAssignedIdentitiesValue from managed identity deployment: %w", err)
	}
	userAssignedIdentitiesProfile, err := ConvertToUserAssignedIdentitiesProfile20251223(userAssignedIdentities)
	if err != nil {
		return params, fmt.Errorf("failed to convert userAssignedIdentitiesValue: %w", err)
	}

	identityValue, err := GetOutputValue(managedIdentitiesDeploymentResult, "identityValue")
	if err != nil {
		return params, fmt.Errorf("failed to get identityValue from managed identity deployment: %w", err)
	}
	identityProfile, err := ConvertToManagedServiceIdentity20251223(identityValue)
	if err != nil {
		return params, fmt.Errorf("failed to convert identityValue: %w", err)
	}

	params.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
	params.Identity = identityProfile

	return params, nil
}

// ---------------------------------------------------------------------------
// Build functions (from hcp_helper.go)
// ---------------------------------------------------------------------------

func BuildHCPClusterFromParams20251223(
	parameters ClusterParams20251223,
	location string,
	imageDigestMirrors []*hcpsdk20251223preview.ImageDigestMirror,
) (hcpsdk20251223preview.HcpOpenShiftCluster, error) {
	// Convert identity types from old SDK to new SDK via JSON round-trip
	var identity *hcpsdk20251223preview.ManagedServiceIdentity
	if parameters.Identity != nil {
		var err error
		identity, err = convertViaJSON[hcpsdk20251223preview.ManagedServiceIdentity](parameters.Identity)
		if err != nil {
			return hcpsdk20251223preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert Identity: %w", err)
		}
	}

	var uamis *hcpsdk20251223preview.UserAssignedIdentitiesProfile
	if parameters.UserAssignedIdentitiesProfile != nil {
		var err error
		uamis, err = convertViaJSON[hcpsdk20251223preview.UserAssignedIdentitiesProfile](parameters.UserAssignedIdentitiesProfile)
		if err != nil {
			return hcpsdk20251223preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to convert UserAssignedIdentitiesProfile: %w", err)
		}
	}

	return hcpsdk20251223preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: identity,
		Tags:     parameters.Tags,
		Properties: &hcpsdk20251223preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20251223preview.VersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Platform: &hcpsdk20251223preview.PlatformProfile{
				ManagedResourceGroup:    to.Ptr(parameters.ManagedResourceGroupName),
				NetworkSecurityGroupID:  to.Ptr(parameters.NsgResourceID),
				SubnetID:                to.Ptr(parameters.SubnetResourceID),
				VnetIntegrationSubnetID: to.Ptr(parameters.VnetIntegrationSubnetID),
				OperatorsAuthentication: &hcpsdk20251223preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: uamis,
				},
			},
			Network: &hcpsdk20251223preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20251223preview.NetworkType(parameters.Network.NetworkType)),
				PodCIDR:     to.Ptr(parameters.Network.PodCIDR),
				ServiceCIDR: to.Ptr(parameters.Network.ServiceCIDR),
				MachineCIDR: to.Ptr(parameters.Network.MachineCIDR),
				HostPrefix:  to.Ptr(parameters.Network.HostPrefix),
			},
			API: &hcpsdk20251223preview.APIProfile{
				Visibility:      to.Ptr(hcpsdk20251223preview.Visibility(parameters.APIVisibility)),
				AuthorizedCIDRs: parameters.AuthorizedCIDRs,
			},
			ClusterImageRegistry: &hcpsdk20251223preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20251223preview.ClusterImageRegistryState(parameters.ImageRegistryState)),
			},
			Etcd: &hcpsdk20251223preview.EtcdProfile{
				DataEncryption: &hcpsdk20251223preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20251223preview.EtcdDataEncryptionKeyManagementModeType(parameters.EncryptionKeyManagementMode)),
					CustomerManaged: &hcpsdk20251223preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20251223preview.CustomerManagedEncryptionType(parameters.EncryptionType)),
						Kms: &hcpsdk20251223preview.KmsEncryptionProfile{
							VaultName:  to.Ptr(parameters.KeyVaultName),
							Visibility: to.Ptr(hcpsdk20251223preview.KeyVaultVisibility(parameters.KeyVaultVisibility)),
							ActiveKey: &hcpsdk20251223preview.KmsKey{
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

func BuildNodePoolFromParams20251223(
	parameters NodePoolParams20251223,
	location string,
) hcpsdk20251223preview.NodePool {

	nodePool := hcpsdk20251223preview.NodePool{
		Location: to.Ptr(location),
		Tags:     parameters.Tags,
		Properties: &hcpsdk20251223preview.NodePoolProperties{
			Version: &hcpsdk20251223preview.NodePoolVersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			NodeDrainTimeoutMinutes: parameters.NodeDrainTimeoutMinutes,
			Platform: &hcpsdk20251223preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(parameters.VMSize),
				OSDisk: &hcpsdk20251223preview.OsDiskProfile{
					SizeGiB:                to.Ptr(parameters.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20251223preview.DiskStorageAccountType(parameters.DiskStorageAccountType)),
					DiskType:               to.Ptr(parameters.DiskType),
				},
				AvailabilityZone: to.Ptr(parameters.AvailabilityZone),
			},
			AutoRepair: to.Ptr(parameters.AutoRepair),
			Labels:     parameters.Labels,
			Taints:     parameters.Taints,
		},
	}

	if parameters.AutoScaling != nil {
		nodePool.Properties.AutoScaling = &hcpsdk20251223preview.NodePoolAutoScaling{
			Min: to.Ptr(parameters.AutoScaling.Min),
			Max: to.Ptr(parameters.AutoScaling.Max),
		}
	} else {
		nodePool.Properties.Replicas = to.Ptr(parameters.Replicas)
	}

	return nodePool
}

// ---------------------------------------------------------------------------
// CRUD operations (from hcp_helper.go)
// ---------------------------------------------------------------------------

// CreateHCPClusterAndWait20251223 creates an HCP cluster using the v20251223preview API and waits for completion.
func CreateHCPClusterAndWait20251223(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20251223preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20251223preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20251223preview.HcpOpenShiftCluster, error) {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20251223AndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
		defer cancel()
	}

	logger.Info("Starting HCP cluster creation (v20251223preview)", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName)
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
		case hcpsdk20251223preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
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

// UpdateHCPCluster20251223 updates an HCP cluster using the v20251223preview SDK and waits for the operation to complete.
// Transient 500 and 409 errors are retried automatically with exponential backoff.
func UpdateHCPCluster20251223(
	ctx context.Context,
	hcpClient *hcpsdk20251223preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20251223preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (*hcpsdk20251223preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateHCPCluster20251223 for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var hcpOpenShiftCluster *hcpsdk20251223preview.HcpOpenShiftCluster
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

		// The v20251223 variant does not call checkOperationResult because
		// the v20251223 SDK does not yet have a matching Get response type
		// to compare against. The v20240610 variant above does this check.
		hcpOpenShiftCluster = &operationResult.HcpOpenShiftCluster
		return true, nil
	})

	if wait.Interrupted(err) && lastTransientErr != nil {
		return hcpOpenShiftCluster, fmt.Errorf("update interrupted for hcpCluster=%q in resourcegroup=%q after %d attempts, last transient error: %w, interrupt cause: %w", hcpClusterName, resourceGroupName, attempt, lastTransientErr, err)
	}

	return hcpOpenShiftCluster, err
}

// CreateNodePoolAndWait20251223 creates a nodepool using the v20251223preview API and waits for completion.
func CreateNodePoolAndWait20251223(
	ctx context.Context,
	nodePoolsClient *hcpsdk20251223preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20251223preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20251223preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolAndWait20251223 for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
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
	expect, err := GetNodePool20251223(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
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

// GetNodePool20251223 retrieves a nodepool using the v20251223preview API.
func GetNodePool20251223(
	ctx context.Context,
	nodePoolsClient *hcpsdk20251223preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (*hcpsdk20251223preview.NodePool, error) {
	resp, err := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		return nil, err
	}
	return &resp.NodePool, nil
}

// ---------------------------------------------------------------------------
// High-level helpers (from deployment_params.go and deployment_helper.go)
// ---------------------------------------------------------------------------

func (tc *perItOrDescribeTestContext) CreateClusterCustomerResources20251223(ctx context.Context,
	resourceGroup *armresources.ResourceGroup,
	clusterParams ClusterParams20251223,
	infraParameters map[string]interface{},
	artifactsFS embed.FS,
	rbacScope RBACScope,
) (ClusterParams20251223, error) {
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
	clusterParams, err = PopulateClusterParamsFromCustomerInfraDeployment20251223(clusterParams, customerInfraDeploymentResult)
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
	clusterParams, err = PopulateClusterParamsFromManagedIdentitiesDeployment20251223(clusterParams, managedIdentityDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from managed identities: %w", err)
	}
	return clusterParams, nil
}

func (tc *perItOrDescribeTestContext) CreateHCPClusterFromParam20251223(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	parameters ClusterParams20251223,
	imageDigestMirrors []*hcpsdk20251223preview.ImageDigestMirror,
	timeout time.Duration,
) error {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPCluster20251223FromParam for cluster %s in resource group %s", timeout.Minutes(), parameters.ClusterName, resourceGroupName))
		defer cancel()
	}
	clusterName := parameters.ClusterName

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy HCP cluster %s/%s (v20251223preview)", resourceGroupName, clusterName), startTime, finishTime)
	}()

	if OnResourceGroupCreated != nil && parameters.ManagedResourceGroupName != "" {
		OnResourceGroupCreated(parameters.ManagedResourceGroupName)
	}
	if OnHCPClusterCreated != nil && parameters.ClusterName != "" {
		OnHCPClusterCreated(resourceGroupName, parameters.ClusterName)
	}

	cluster, err := BuildHCPClusterFromParams20251223(parameters, tc.Location(), imageDigestMirrors)
	if err != nil {
		return fmt.Errorf("failed to build HCP cluster %s: %w", clusterName, err)
	}

	deployedCluster, err := CreateHCPClusterAndWait20251223(
		ctx,
		logger,
		tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		resourceGroupName,
		clusterName,
		cluster,
		timeout,
	)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed to create HCP cluster %s, caused by: %w, error: %w", clusterName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed to create HCP cluster %s: %w", clusterName, err)
	}
	if OnResourceGroupCreated != nil && parameters.ManagedResourceGroupName == "" &&
		deployedCluster != nil && deployedCluster.Properties != nil &&
		deployedCluster.Properties.Platform != nil &&
		deployedCluster.Properties.Platform.ManagedResourceGroup != nil {
		OnResourceGroupCreated(*deployedCluster.Properties.Platform.ManagedResourceGroup)
	}
	return nil
}

func (tc *perItOrDescribeTestContext) CreateNodePoolFromParam20251223(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	managedResourceGroupName string,
	hcpClusterName string,
	parameters NodePoolParams20251223,
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

	nodePool := BuildNodePoolFromParams20251223(parameters, tc.Location())

	if _, err := CreateNodePoolAndWait20251223(
		nodePoolCtx,
		tc.Get20251223ClientFactoryOrDie(nodePoolCtx).NewNodePoolsClient(),
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
// Client factory methods (from per_test_framework.go)
// ---------------------------------------------------------------------------

func (tc *perItOrDescribeTestContext) Get20251223ClientFactory(ctx context.Context) (*hcpsdk20251223preview.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20251223 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20251223, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20251223ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) Get20251223ClientFactoryOrDie(ctx context.Context) *hcpsdk20251223preview.ClientFactory {
	return Must(tc.Get20251223ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) get20251223ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20251223preview.ClientFactory, error) {
	if tc.clientFactory20251223 != nil {
		return tc.clientFactory20251223, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpsdk20251223preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20251223 = clientFactory

	return tc.clientFactory20251223, nil
}
