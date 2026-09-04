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
	"net/http"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// ========================================================================
// Types from deployment_params.go
// ========================================================================

type ClusterParams20240610 struct {
	OpenshiftVersionId            string
	ClusterName                   string
	ManagedResourceGroupName      string
	NsgResourceID                 string
	NsgName                       string
	SubnetResourceID              string
	SubnetName                    string
	VnetName                      string
	UserAssignedIdentitiesProfile *hcpsdk20240610preview.UserAssignedIdentitiesProfile
	Identity                      *hcpsdk20240610preview.ManagedServiceIdentity
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
	Autoscaling                   *hcpsdk20240610preview.ClusterAutoscalingProfile
	Tags                          map[string]*string
}

type NodePoolParams20240610 struct {
	OpenshiftVersionId     string
	ClusterName            string
	NodePoolName           string
	Replicas               int32
	VMSize                 string
	OSDiskSizeGiB          int32
	DiskStorageAccountType string
	ChannelGroup           string
	// NodeDrainTimeoutMinutes: how long (in minutes) to respect Pod Disruption Budgets when draining
	// nodes in this pool (e.g. upgrades, scale-in). Valid: 0 to 10080. 0 = no time limit for that phase.
	// When omitted from the create payload or nil here, the cluster-configured global nodeDrainTimeoutMinutes kicks in.
	NodeDrainTimeoutMinutes *int32
	// AutoScaling enables nodepool autoscaling. When set, Replicas is ignored.
	AutoScaling      *NodePoolAutoScalingParams
	AvailabilityZone string
	Tags             map[string]*string
}

// ========================================================================
// Functions from deployment_params.go
// ========================================================================

func NewDefaultClusterParams20240610() ClusterParams20240610 {
	params := ClusterParams20240610{
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

func NewDefaultNodePoolParams20240610() NodePoolParams20240610 {
	return NodePoolParams20240610{
		OpenshiftVersionId: DefaultOpenshiftNodePoolVersionId(),
		Replicas:           int32(2),
		// VMSize is intentionally left empty: CreateNodePoolFromParam20240610
		// resolves it via the restriction-aware DefaultWorkerVMSizeSelector at
		// create time so the suite is resilient to per-subscription SKU
		// restrictions. Set it explicitly to pin a specific size.
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

func ConvertToUserAssignedIdentitiesProfile20240610(value interface{}) (*hcpsdk20240610preview.UserAssignedIdentitiesProfile, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal UserAssignedIdentitiesValue: %w", err)
	}
	var uamis hcpsdk20240610preview.UserAssignedIdentitiesProfile
	if err := json.Unmarshal(b, &uamis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal UserAssignedIdentitiesValue: %w", err)
	}
	return &uamis, nil
}

// ClearUserAssignedIdentityValues20240610 resets every value in a
// ManagedServiceIdentity's UserAssignedIdentities map to an empty struct,
// preserving only the map keys (identity resource IDs).
//
// ARM requires that on a PUT of an existing resource, UserAssignedIdentities
// map values for identities that should be kept unchanged are sent back as
// empty objects ({}); the client/PrincipalID values a prior GET populated
// must not be echoed back. Callers that Get a cluster, mutate an unrelated
// field, and then BeginCreateOrUpdate the full object must call this first
// or ARM rejects the request with error code InvalidIdentityValues.
func ClearUserAssignedIdentityValues20240610(identity *hcpsdk20240610preview.ManagedServiceIdentity) {
	if identity == nil {
		return
	}
	for id := range identity.UserAssignedIdentities {
		identity.UserAssignedIdentities[id] = &hcpsdk20240610preview.UserAssignedIdentity{}
	}
}

func ConvertToManagedServiceIdentity20240610(value interface{}) (*hcpsdk20240610preview.ManagedServiceIdentity, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IdentityValue: %w", err)
	}
	var msi hcpsdk20240610preview.ManagedServiceIdentity
	if err := json.Unmarshal(b, &msi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal IdentityValue: %w", err)
	}
	return &msi, nil
}

func PopulateClusterParamsFromCustomerInfraDeployment20240610(
	params ClusterParams20240610,
	customerInfraDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20240610, error) {
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

func PopulateClusterParamsFromManagedIdentitiesDeployment20240610(
	params ClusterParams20240610,
	managedIdentitiesDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams20240610, error) {
	if managedIdentitiesDeploymentResult == nil {
		return params, fmt.Errorf("managedIdentitiesDeploymentResult cannot be nil")
	}

	userAssignedIdentities, err := GetOutputValue(managedIdentitiesDeploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		return params, fmt.Errorf("failed to get userAssignedIdentitiesValue from managed identity deployment: %w", err)
	}
	userAssignedIdentitiesProfile, err := ConvertToUserAssignedIdentitiesProfile20240610(userAssignedIdentities)
	if err != nil {
		return params, fmt.Errorf("failed to convert userAssignedIdentitiesValue: %w", err)
	}

	identityValue, err := GetOutputValue(managedIdentitiesDeploymentResult, "identityValue")
	if err != nil {
		return params, fmt.Errorf("failed to get identityValue from managed identity deployment: %w", err)
	}
	identityProfile, err := ConvertToManagedServiceIdentity20240610(identityValue)
	if err != nil {
		return params, fmt.Errorf("failed to convert identityValue: %w", err)
	}

	params.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
	params.Identity = identityProfile

	return params, nil
}

func (tc *perItOrDescribeTestContext) CreateClusterCustomerResources20240610(ctx context.Context,
	resourceGroup *armresources.ResourceGroup,
	clusterParams ClusterParams20240610,
	infraParameters map[string]interface{},
	artifactsFS embed.FS,
	rbacScope RBACScope,
) (ClusterParams20240610, error) {
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
	clusterParams, err = PopulateClusterParamsFromCustomerInfraDeployment20240610(clusterParams, customerInfraDeploymentResult)
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
	clusterParams, err = PopulateClusterParamsFromManagedIdentitiesDeployment20240610(clusterParams, managedIdentityDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from managed identities: %w", err)
	}
	return clusterParams, nil
}

// ========================================================================
// Functions from deployment_helper.go
// ========================================================================

func (tc *perItOrDescribeTestContext) CreateHCPClusterFromParam20240610(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	parameters ClusterParams20240610,
	timeout time.Duration,
) error {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPClusterFromParam for cluster %s in resource group %s", timeout.Minutes(), parameters.ClusterName, resourceGroupName))
		defer cancel()
	}
	clusterName := parameters.ClusterName

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy HCP cluster %s/%s", resourceGroupName, clusterName), startTime, finishTime)
	}()

	cluster := BuildHCPClusterFromParams20240610(parameters, tc.Location())

	if _, err := CreateHCPClusterAndWait20240610(
		ctx,
		logger,
		tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
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

func (tc *perItOrDescribeTestContext) CreateNodePoolFromParam20240610(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	managedResourceGroupName string,
	hcpClusterName string,
	parameters NodePoolParams20240610,
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

	nodePool := BuildNodePoolFromParams20240610(parameters, tc.Location())

	if _, err := CreateNodePoolAndWait20240610(
		nodePoolCtx,
		tc.Get20240610ClientFactoryOrDie(nodePoolCtx).NewNodePoolsClient(),
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

// ========================================================================
// Functions from hcp_helper.go
// ========================================================================

func (tc *perItOrDescribeTestContext) GetAdminRESTConfigForHCPCluster20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration, // this is a POST request, so keep the timeout as it's async
) (*rest.Config, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during GetAdminRESTConfigForHCPCluster for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Collect admin credentials for cluster", startTime, finishTime)
	}()

	adminCredentialRequestPoller, err := hcpClient.BeginRequestAdminCredential(
		ctx,
		resourceGroupName,
		hcpClusterName,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start credential request: %w", err)
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

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse:
		restConfig, err := clientcmd.BuildConfigFromKubeconfigGetter("", func() (*clientcmdapi.Config, error) {
			if m.Kubeconfig == nil {
				return nil, fmt.Errorf("kubeconfig content is nil")
			}
			return clientcmd.Load([]byte(*m.Kubeconfig))
		})
		if err != nil {
			return nil, err
		}

		tc.contextLock.Lock()
		tc.hcpAdminConfigs[resourceGroupName+"/"+hcpClusterName] = restConfig
		tc.contextLock.Unlock()

		return restConfig, nil
	default:
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

func (tc *perItOrDescribeTestContext) RevokeCredentialsAndWait20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during RevokeCredentialsAndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Collect revoke admin credentials for cluster", startTime, finishTime)
	}()

	poller, err := hcpClient.BeginRevokeCredentials(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to start credential revocation for hcpCluster=%q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish revoking creds, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish revoking creds: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientRevokeCredentialsResponse:
		return nil
	default:
		return fmt.Errorf("unknown type %T", m)
	}
}

// deleteHCPClusterAttempt performs a single delete attempt for an HCP cluster.
func deleteHCPClusterAttempt(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
) error {
	poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
			resp, getErr := hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
			if getErr == nil && resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateDeleting {
				ginkgo.GinkgoLogr.Info("cluster already deleting, waiting for completion",
					"cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return waitForHCPClusterDeletion20240610(ctx, hcpClient, resourceGroupName, hcpClusterName)
			}
		}
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse:
	default:
		ginkgo.GinkgoLogr.Info("unexpected operation result", "type", fmt.Sprintf("%T", m), "content", spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// DeleteHCPCluster20240610 deletes an hcp cluster and waits for the operation to complete
// Transient ConflictingConcurrentWriteNotAllowed (409) errors are retried automatically
// with exponential backoff.
func DeleteHCPCluster20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteHCPCluster for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var attempt int
	var lastTransientErr error
	retryErr := wait.ExponentialBackoffWithContext(ctx, stateConflictBackoff, func(ctx context.Context) (bool, error) {
		attempt++
		err := deleteHCPClusterAttempt(ctx, hcpClient, resourceGroupName, hcpClusterName)
		if err == nil {
			return true, nil
		}
		if isTransientDeleteError(err) {
			lastTransientErr = err
			ginkgo.GinkgoLogr.Info("transient conflict during cluster delete, retrying",
				"cluster", hcpClusterName, "resourceGroup", resourceGroupName,
				"attempt", attempt, "error", err.Error())
			return false, nil
		}
		return false, err
	})

	if wait.Interrupted(retryErr) && lastTransientErr != nil {
		return fmt.Errorf("delete interrupted for hcpCluster=%q in resourcegroup=%q after %d attempts, last transient error: %w, interrupt cause: %w", hcpClusterName, resourceGroupName, attempt, lastTransientErr, retryErr)
	}

	return retryErr
}

// waitForHCPClusterDeletion20240610 polls GET on the cluster until it returns 404 (deleted).
func waitForHCPClusterDeletion20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
) error {
	for {
		_, err := hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				ginkgo.GinkgoLogr.Info("cluster deletion completed",
					"cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for already-deleting hcpCluster=%q in resourcegroup=%q to be deleted, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return fmt.Errorf("failed polling for deletion of hcpCluster=%q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for already-deleting hcpCluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), ctx.Err())
		case <-time.After(StandardPollInterval):
		}
	}
}

// UpdateHCPCluster20240610 updates an HCP cluster using the v20240610preview SDK and waits for the operation to complete.
// Transient 500, 409, and CS state conflict 400 errors are retried automatically with exponential backoff.
func UpdateHCPCluster20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20240610preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (*hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateHCPCluster for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
	defer cancel()

	var hcpOpenShiftCluster *hcpsdk20240610preview.HcpOpenShiftCluster
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

		switch m := any(operationResult).(type) {
		case hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse:
			expect, err := GetHCPCluster20240610(ctx, hcpClient, resourceGroupName, hcpClusterName)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return false, fmt.Errorf("failed getting hcpCluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
				}
				return false, err
			}
			if err := checkOperationResult(&expect.HcpOpenShiftCluster, &m.HcpOpenShiftCluster); err != nil {
				return false, err
			}
			hcpOpenShiftCluster = &m.HcpOpenShiftCluster
			return true, nil
		default:
			return false, fmt.Errorf("unknown type %T", m)
		}
	})

	if wait.Interrupted(err) && lastTransientErr != nil {
		return hcpOpenShiftCluster, fmt.Errorf("update interrupted for hcpCluster=%q in resourcegroup=%q after %d attempts, last transient error: %w, interrupt cause: %w", hcpClusterName, resourceGroupName, attempt, lastTransientErr, err)
	}

	return hcpOpenShiftCluster, err
}

// GetHCPCluster20240610 fetches an HCP cluster
func GetHCPCluster20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	return hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
}

// DeleteAllHCPClusters20240610 deletes all Clusters within a resource group and waits
func DeleteAllHCPClusters20240610(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteAllHCPClusters for resource group %s", timeout.Minutes(), resourceGroupName))
	defer cancel()

	var hcpClustersWithoutSizeTag []string
	hcpClusterNames := []string{}
	hcpClusterPager := hcpClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for hcpClusterPager.More() {
		page, err := hcpClusterPager.NextPage(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), err)
			}
			return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q: %w", resourceGroupName, err)
		}
		for _, cluster := range page.Value {
			hcpClusterNames = append(hcpClusterNames, *cluster.Name)
			if value, set := cluster.Tags[metadataapi.TagClusterSizeOverride]; !set || value == nil || *value != string(coreapi.MinimalControlPlanePodSizing) {
				hcpClustersWithoutSizeTag = append(hcpClustersWithoutSizeTag, *cluster.Name)
			}
		}
	}

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, hcpClusterName := range hcpClusterNames {
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			defer utilruntime.HandleCrashWithContext(ctx)

			return DeleteHCPCluster20240610(ctx, hcpClient, resourceGroupName, hcpClusterName, HCPClusterDeletionTimeout)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed deleting hcp clusters in resourcegroup=%q, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), err)
		}
		// remember that Wait only shows the first error, not all the errors.
		return fmt.Errorf("at least one hcp cluster failed to delete: %w", err)
	}

	if len(hcpClustersWithoutSizeTag) > 0 {
		return &NonConformingClustersError{clusters: hcpClustersWithoutSizeTag}
	}

	return nil
}

// DeleteNodePool20240610 deletes a nodepool and waits for the operation to complete
func DeleteNodePool20240610(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteNodePool for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()

	poller, err := nodePoolsClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
			resp, getErr := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
			if getErr == nil && resp.Properties != nil && resp.Properties.ProvisioningState != nil && *resp.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateDeleting {
				ginkgo.GinkgoLogr.Info("nodepool already deleting, waiting for completion",
					"nodePool", nodePoolName, "cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return waitForNodePoolDeletion20240610(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed starting nodepool deletion %q for cluster %q in resourcegroup=%q, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish deleting, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish deleting: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientDeleteResponse:
	default:
		ginkgo.GinkgoLogr.Info("unexpected operation result", "type", fmt.Sprintf("%T", m), "content", spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// waitForNodePoolDeletion20240610 polls GET on the nodepool until it returns 404 (deleted).
func waitForNodePoolDeletion20240610(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) error {
	for {
		_, err := nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				ginkgo.GinkgoLogr.Info("nodepool deletion completed",
					"nodePool", nodePoolName, "cluster", hcpClusterName, "resourceGroup", resourceGroupName)
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for already-deleting nodepool=%q in cluster=%q resourcegroup=%q to be deleted, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return fmt.Errorf("failed polling for deletion of nodepool=%q in cluster=%q resourcegroup=%q: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for already-deleting nodepool=%q in cluster=%q resourcegroup=%q, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), ctx.Err())
		case <-time.After(StandardPollInterval):
		}
	}
}

// GetNodePool20240610 fetches a nodepool resource
func GetNodePool20240610(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	return nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
}

// UpdateNodePoolAndWait20240610 sends a PATCH (BeginUpdate) request for a nodepool and waits for completion
// within the provided timeout. It returns the final update response or an error.
func UpdateNodePoolAndWait20240610(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	update hcpsdk20240610preview.NodePoolUpdate,
	timeout time.Duration,
) (*hcpsdk20240610preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during UpdateNodePoolAndWait for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
	defer cancel()

	poller, err := nodePoolsClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, nodePoolName, update, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start nodepool %q update in cluster %q resourcegroup=%q: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish updating, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish updating: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientUpdateResponse:
		expect, err := GetNodePool20240610(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed getting nodepool=%q in cluster=%q resourcegroup=%q, caused by: %w, error: %w", nodePoolName, hcpClusterName, resourceGroupName, context.Cause(ctx), err)
			}
			return nil, err
		}
		err = checkOperationResult(&expect.NodePool, &m.NodePool)
		if err != nil {
			return nil, err
		}
		return &m.NodePool, nil
	default:
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// CreateOrUpdateExternalAuthAndWait20240610 creates or updates an external auth on an HCP cluster and waits
func CreateOrUpdateExternalAuthAndWait20240610(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
	externalAuth hcpsdk20240610preview.ExternalAuth,
	timeout time.Duration,
) (*hcpsdk20240610preview.ExternalAuth, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateOrUpdateExternalAuthAndWait for external auth %s in cluster %s in resource group %s", timeout.Minutes(), externalAuthName, hcpClusterName, resourceGroupName))
	defer cancel()

	pollerResp, err := externalAuthClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		hcpClusterName,
		externalAuthName,
		externalAuth,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating external auth %q in resourcegroup=%q for cluster=%q: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}
	operationResult, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish creating or updating, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
		}
		return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish creating or updating: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientCreateOrUpdateResponse:
		// Verify the operationResult content matches the current external auth model.
		// When an asynchronous operation completes successfully, the RP's result
		// endpoint for the operation is supposed to respond as though the operation
		// were completed synchronously. In production, ARM would call this endpoint
		// automatically. In this context, the poller calls it automatically.
		expect, err := GetExternalAuth20240610(ctx, externalAuthClient, resourceGroupName, hcpClusterName, externalAuthName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed getting external auth %q in resourcegroup=%q for cluster=%q, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed getting external auth %q in resourcegroup=%q for cluster=%q: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
		}
		err = checkOperationResult(&expect.ExternalAuth, &m.ExternalAuth)
		if err != nil {
			return nil, err
		}
		return &m.ExternalAuth, nil
	default:
		ginkgo.GinkgoLogr.Info("unexpected operation result", "type", fmt.Sprintf("%T", m), "content", spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// GetExternalAuth20240610 fetches an external auth resource
func GetExternalAuth20240610(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
) (hcpsdk20240610preview.ExternalAuthsClientGetResponse, error) {
	return externalAuthClient.Get(
		ctx,
		resourceGroupName,
		hcpClusterName,
		externalAuthName,
		&hcpsdk20240610preview.ExternalAuthsClientGetOptions{},
	)
}

// DeleteExternalAuthAndWait20240610 deletes a an external auth on an HCP cluster and waits
func DeleteExternalAuthAndWait20240610(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during DeleteExternalAuthAndWait for external auth %s in cluster %s in resource group %s", timeout.Minutes(), externalAuthName, hcpClusterName, resourceGroupName))
	defer cancel()

	pollerResp, err := externalAuthClient.BeginDelete(
		ctx,
		resourceGroupName,
		hcpClusterName,
		externalAuthName,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed deleting external auth %q in resourcegroup=%q for cluster=%q: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}
	operationResult, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish deleting, caused by: %w, error: %w", externalAuthName, resourceGroupName, hcpClusterName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish deleting: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientDeleteResponse:
		return nil
	default:
		ginkgo.GinkgoLogr.Info("unexpected operation result", "type", fmt.Sprintf("%T", m), "content", spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}
}

func BeginCreateHCPCluster20240610(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	clusterParams ClusterParams20240610,
	location string,
) (*runtime.Poller[hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse], error) {
	cluster := BuildHCPClusterFromParams20240610(clusterParams, location)
	logger.Info("Starting HCP cluster creation", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName)
	poller, err := hcpClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, cluster, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting cluster creation %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}
	return poller, nil
}

// CreateHCPClusterAndWait20240610 Note that the timeout parameter will only take effect if its value is greater than 0. Otherwise,
// the function won't wait for the deployment to be ready.
func CreateHCPClusterAndWait20240610(
	ctx context.Context,
	logger logr.Logger,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20240610preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPClusterAndWait for cluster %s in resource group %s", timeout.Minutes(), hcpClusterName, resourceGroupName))
		defer cancel()
	}

	logger.Info("Starting HCP cluster creation", "clusterName", hcpClusterName, "resourceGroup", resourceGroupName, "version", cluster.Properties.Version.ID, "channelGroup", cluster.Properties.Version.ChannelGroup)
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
		case hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
			// Verify the operationResult content matches the current cluster model.
			// When an asynchronous operation completes successfully, the RP's result
			// endpoint for the operation is supposed to respond as though the operation
			// were completed synchronously. In production, ARM would call this endpoint
			// automatically. In this context, the poller calls it automatically.
			expect, err := GetHCPCluster20240610(ctx, hcpClient, resourceGroupName, hcpClusterName)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, fmt.Errorf("failed getting cluster=%q in resourcegroup=%q, caused by: %w, error: %w", hcpClusterName, resourceGroupName, context.Cause(ctx), err)
				}
				return nil, err
			}
			err = checkOperationResult(&expect.HcpOpenShiftCluster, &m.HcpOpenShiftCluster)
			if err != nil {
				return nil, err
			}
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

func BuildHCPClusterFromParams20240610(
	parameters ClusterParams20240610,
	location string,
) hcpsdk20240610preview.HcpOpenShiftCluster {

	return hcpsdk20240610preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: parameters.Identity,
		Tags:     parameters.Tags,
		Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20240610preview.VersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			Platform: &hcpsdk20240610preview.PlatformProfile{
				ManagedResourceGroup:   to.Ptr(parameters.ManagedResourceGroupName),
				NetworkSecurityGroupID: to.Ptr(parameters.NsgResourceID),
				SubnetID:               to.Ptr(parameters.SubnetResourceID),
				OperatorsAuthentication: &hcpsdk20240610preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: parameters.UserAssignedIdentitiesProfile,
				}},
			Network: &hcpsdk20240610preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20240610preview.NetworkType(parameters.Network.NetworkType)),
				PodCIDR:     to.Ptr(parameters.Network.PodCIDR),
				ServiceCIDR: to.Ptr(parameters.Network.ServiceCIDR),
				MachineCIDR: to.Ptr(parameters.Network.MachineCIDR),
				HostPrefix:  to.Ptr(parameters.Network.HostPrefix),
			},
			API: &hcpsdk20240610preview.APIProfile{
				Visibility:      to.Ptr(hcpsdk20240610preview.Visibility(parameters.APIVisibility)),
				AuthorizedCIDRs: parameters.AuthorizedCIDRs,
			},
			ClusterImageRegistry: &hcpsdk20240610preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20240610preview.ClusterImageRegistryState(parameters.ImageRegistryState)),
			},
			Etcd: &hcpsdk20240610preview.EtcdProfile{
				DataEncryption: &hcpsdk20240610preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20240610preview.EtcdDataEncryptionKeyManagementModeType(parameters.EncryptionKeyManagementMode)),
					CustomerManaged: &hcpsdk20240610preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20240610preview.CustomerManagedEncryptionType(parameters.EncryptionType)),
						Kms: &hcpsdk20240610preview.KmsEncryptionProfile{
							ActiveKey: &hcpsdk20240610preview.KmsKey{
								VaultName: to.Ptr(parameters.KeyVaultName),
								Name:      to.Ptr(parameters.EtcdEncryptionKeyName),
								Version:   to.Ptr(parameters.EtcdEncryptionKeyVersion),
							},
						},
					},
				},
			},
			Autoscaling: parameters.Autoscaling,
		},
	}
}

func CreateNodePoolAndWait20240610(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20240610preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20240610preview.NodePool, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolAndWait for nodepool %s in cluster %s in resource group %s", timeout.Minutes(), nodePoolName, hcpClusterName, resourceGroupName))
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
	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse:
		// Verify the operationResult content matches the current node pool model.
		// When an asynchronous operation completes successfully, the RP's result
		// endpoint for the operation is supposed to respond as though the operation
		// were completed synchronously. In production, ARM would call this endpoint
		// automatically. In this context, the poller calls it automatically.
		expect, err := GetNodePool20240610(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed to get nodepool, caused by: %w, error: %w", context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed to get nodepool %q in cluster %q resourcegroup=%q after creation: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
		}
		err = checkOperationResult(&expect.NodePool, &m.NodePool)
		if err != nil {
			return nil, err
		}
		return &m.NodePool, nil
	default:
		ginkgo.GinkgoLogr.Info("unexpected operation result", "type", fmt.Sprintf("%T", m), "content", spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

func BuildNodePoolFromParams20240610(
	parameters NodePoolParams20240610,
	location string,
) hcpsdk20240610preview.NodePool {

	nodePool := hcpsdk20240610preview.NodePool{
		Location: to.Ptr(location),
		Tags:     parameters.Tags,
		Properties: &hcpsdk20240610preview.NodePoolProperties{
			Version: &hcpsdk20240610preview.NodePoolVersionProfile{
				ID:           to.Ptr(parameters.OpenshiftVersionId),
				ChannelGroup: to.Ptr(parameters.ChannelGroup),
			},
			NodeDrainTimeoutMinutes: parameters.NodeDrainTimeoutMinutes,
			Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(parameters.VMSize),
				OSDisk: &hcpsdk20240610preview.OsDiskProfile{
					SizeGiB:                to.Ptr(parameters.OSDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20240610preview.DiskStorageAccountType(parameters.DiskStorageAccountType)),
				},
				AvailabilityZone: to.Ptr(parameters.AvailabilityZone),
			},
		},
	}

	if parameters.AutoScaling != nil {
		nodePool.Properties.AutoScaling = &hcpsdk20240610preview.NodePoolAutoScaling{
			Min: to.Ptr(parameters.AutoScaling.Min),
			Max: to.Ptr(parameters.AutoScaling.Max),
		}
	} else {
		nodePool.Properties.Replicas = to.Ptr(parameters.Replicas)
	}

	return nodePool
}

// Verifies that a nodepool created using framework has DiskStorageAccountType set to the framework default "StandardSSD_LRS"
func ValidateNodePoolDiskStorageAccountType20240610(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) error {
	nodePoolResp, err := GetNodePool20240610(ctx, nodePoolsClient, resourceGroupName, hcpClusterName, nodePoolName)
	if err != nil {
		return fmt.Errorf("failed to get nodepool %s: %w", nodePoolName, err)
	}

	nodePool := nodePoolResp.NodePool

	// Verify the nodepool exists and has the expected structure
	if nodePool.Properties == nil {
		return fmt.Errorf("nodepool %s has no properties", nodePoolName)
	}

	if nodePool.Properties.Platform == nil {
		return fmt.Errorf("nodepool %s has no platform configuration", nodePoolName)
	}

	if nodePool.Properties.Platform.OSDisk == nil {
		return fmt.Errorf("nodepool %s has no OS disk configuration", nodePoolName)
	}

	if nodePool.Properties.Platform.OSDisk.DiskStorageAccountType == nil {
		return fmt.Errorf("nodepool %s has no DiskStorageAccountType set", nodePoolName)
	}

	// Verify the framework default (StandardSSD_LRS) overrode the API default (Premium_LRS)
	expectedDiskType := "StandardSSD_LRS"
	actualDiskType := string(*nodePool.Properties.Platform.OSDisk.DiskStorageAccountType)

	if actualDiskType != expectedDiskType {
		return fmt.Errorf("nodepool %s has incorrect DiskStorageAccountType: expected %s (framework default), got %s",
			nodePoolName, expectedDiskType, actualDiskType)
	}

	return nil
}

// ========================================================================
// Functions from per_test_framework.go
// ========================================================================

func (tc *perItOrDescribeTestContext) Get20240610ClientFactoryOrDie(ctx context.Context) *hcpsdk20240610preview.ClientFactory {
	return Must(tc.Get20240610ClientFactory(ctx))
}

func (tc *perItOrDescribeTestContext) Get20240610ClientFactory(ctx context.Context) (*hcpsdk20240610preview.ClientFactory, error) {
	tc.contextLock.RLock()
	if tc.clientFactory20240610 != nil {
		defer tc.contextLock.RUnlock()
		return tc.clientFactory20240610, nil
	}
	tc.contextLock.RUnlock()

	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	return tc.get20240610ClientFactoryUnlocked(ctx)
}

func (tc *perItOrDescribeTestContext) get20240610ClientFactoryUnlocked(ctx context.Context) (*hcpsdk20240610preview.ClientFactory, error) {
	if tc.clientFactory20240610 != nil {
		return tc.clientFactory20240610, nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	clientFactory, err := hcpsdk20240610preview.NewClientFactory(subscriptionID, creds, tc.perBinaryInvocationTestContext.getHCPClientFactoryOptions())
	if err != nil {
		return nil, err
	}
	tc.clientFactory20240610 = clientFactory

	return tc.clientFactory20240610, nil
}
