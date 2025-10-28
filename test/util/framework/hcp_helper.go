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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"golang.org/x/sync/errgroup"

	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func GetAdminRESTConfigForHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration, // this is a POST request, so keep the timeout as it's async
) (*rest.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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
		return nil, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish getting creds: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse:
		return readStaticRESTConfig(m.Kubeconfig)
	default:
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

func readStaticRESTConfig(kubeconfigContent *string) (*rest.Config, error) {
	ret, err := clientcmd.BuildConfigFromKubeconfigGetter("", func() (*clientcmdapi.Config, error) {
		if kubeconfigContent == nil {
			return nil, fmt.Errorf("kubeconfig content is nil")
		}
		return clientcmd.Load([]byte(*kubeconfigContent))
	})
	if err != nil {
		return nil, err
	}

	// Skip TLS verification for development environments with self-signed certificates
	if IsDevelopmentEnvironment() {
		ret.Insecure = true
		ret.CAData = nil
		ret.CAFile = ""
	}

	return ret, nil
}

// DeleteHCPCluster deletes an hcp cluster and waits for the operation to complete
func DeleteHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// UpdateHCPCluster sends a PATCH (BeginUpdate) request for an HCP cluster and waits for completion
// within the provided timeout. It returns the final update response or an error.
func UpdateHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	update hcpsdk20240610preview.HcpOpenShiftClusterUpdate,
	timeout time.Duration,
) (hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := hcpClient.BeginUpdate(ctx, resourceGroupName, hcpClusterName, update, nil)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish updating: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse:
		return m, nil
	default:
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, fmt.Errorf("unknown type %T", m)
	}
}

// GetHCPCluster fetches an HCP cluster
func GetHCPCluster(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	return hcpClient.Get(ctx, resourceGroupName, hcpClusterName, nil)
}

// DeleteAllHCPClusters deletes all HCPOpenShiftClusters within a resource group and waits
func DeleteAllHCPClusters(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hcpClusterNames := []string{}
	hcpClusterPager := hcpClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for hcpClusterPager.More() {
		page, err := hcpClusterPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q: %w", resourceGroupName, err)
		}
		for _, sub := range page.Value {
			hcpClusterNames = append(hcpClusterNames, *sub.Name)
		}
	}

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, hcpClusterName := range hcpClusterNames {
		// https://golang.org/doc/faq#closures_and_goroutines
		hcpClusterName := hcpClusterName
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics to function.
			utilruntime.HandleCrashWithContext(ctx)

			return DeleteHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName, timeout)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		return fmt.Errorf("at least one hcp cluster failed to delete: %w", err)
	}

	return nil
}

// DeleteNodePool deletes a nodepool and waits for the operation to complete
func DeleteNodePool(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := nodePoolsClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
	if err != nil {
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return fmt.Errorf("failed waiting for nodepool=%q in cluster=%q resourcegroup=%q to finish deleting: %w", nodePoolName, hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.NodePoolsClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// GetNodePool fetches a nodepool resource
func GetNodePool(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	return nodePoolsClient.Get(ctx, resourceGroupName, hcpClusterName, nodePoolName, nil)
}

// CreateOrUpdateExternalAuthAndWait creates or updates an external auth on an HCP cluster and waits
func CreateOrUpdateExternalAuthAndWait(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
	externalAuth hcpsdk20240610preview.ExternalAuth,
	timeout time.Duration,
) (*hcpsdk20240610preview.ExternalAuth, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
		return nil, fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientCreateOrUpdateResponse:
		// TODO someone may want this return value.  We'll have to work it out then.
		//fmt.Printf("#### got back: %v\n", spew.Sdump(m))
		return &m.ExternalAuth, nil
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// CreateExternalAuthAndWait creates a an external auth on an HCP cluster and waits
func GetExternalAuth(
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

// DeleteExternalAuthAndWait deletes a an external auth on an HCP cluster and waits
func DeleteExternalAuthAndWait(
	ctx context.Context,
	externalAuthClient *hcpsdk20240610preview.ExternalAuthsClient,
	resourceGroupName string,
	hcpClusterName string,
	externalAuthName string,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
		return fmt.Errorf("failed waiting for external auth %q in resourcegroup=%q for cluster=%q to finish deleting: %w", externalAuthName, resourceGroupName, hcpClusterName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.ExternalAuthsClientDeleteResponse:
		return nil
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}
}

func CreateClusterRoleBinding(ctx context.Context, subject string, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "entra-admins",
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []v1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "User",
				Name:     subject,
			},
		},
	}, metav1.CreateOptions{})

	if err != nil {
		return fmt.Errorf("failed to create cluster role binding: %w", err)
	}

	return nil
}

// CreateHCPClusterAndWait creates an HCP cluster via direct API call and waits for completion , this is to support lower environments .
func CreateHCPClusterAndWait(
	ctx context.Context,
	hcpClient *hcpsdk20240610preview.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	cluster hcpsdk20240610preview.HcpOpenShiftCluster,
	timeout time.Duration,
) (*hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := hcpClient.BeginCreateOrUpdate(ctx, resourceGroupName, hcpClusterName, cluster, nil)
	if err != nil {
		return nil, fmt.Errorf("failed starting cluster creation %q in resourcegroup=%q: %w", hcpClusterName, resourceGroupName, err)
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for cluster=%q in resourcegroup=%q to finish creating: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse:
		return &m.HcpOpenShiftCluster, nil
	default:
		fmt.Printf("unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// BuildHCPClusterFromBicepTemplate converts bicep template and parameters to an HCP cluster object
func BuildHCPClusterFromBicepTemplate(
	ctx context.Context,
	parameters map[string]interface{},
	location string,
	subscriptionId string,
	resourceGroupName string,
	testContext *perItOrDescribeTestContext,
) (hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	// Create HCP cluster struct directly from parameters
	cluster := hcpsdk20240610preview.HcpOpenShiftCluster{
		Location:   &location,
		Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{},
	}
	cluster.Properties.Platform = &hcpsdk20240610preview.PlatformProfile{}
	if openshiftVersionId, ok := parameters["openshiftVersionId"].(string); ok {
		cluster.Properties.Version = &hcpsdk20240610preview.VersionProfile{
			ID: &openshiftVersionId,
		}
		if cluster.Properties.Version.ChannelGroup == nil {
			cluster.Properties.Version.ChannelGroup = to.Ptr("stable")
		}
	}
	if managedResourceGroupName, ok := parameters["managedResourceGroupName"].(string); ok {
		cluster.Properties.Platform.ManagedResourceGroup = &managedResourceGroupName
	}
	if nsgName, ok := parameters["nsgName"].(string); ok {
		nsgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s", subscriptionId, resourceGroupName, nsgName)
		cluster.Properties.Platform.NetworkSecurityGroupID = &nsgID
	}
	if subnetName, ok := parameters["subnetName"].(string); ok {
		if vnetName, ok := parameters["vnetName"].(string); ok {
			subnetID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s", subscriptionId, resourceGroupName, vnetName, subnetName)
			cluster.Properties.Platform.SubnetID = &subnetID
		}
	}
	if uamiVal, ok := parameters["userAssignedIdentitiesValue"]; ok {
		if b, err := json.Marshal(uamiVal); err == nil {
			var uamis hcpsdk20240610preview.UserAssignedIdentitiesProfile
			if err := json.Unmarshal(b, &uamis); err == nil {
				cluster.Properties.Platform.OperatorsAuthentication = &hcpsdk20240610preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: &uamis,
				}
			}
		}
	}
	if kv, ok := parameters["keyVaultName"].(string); ok {
		if key, ok := parameters["etcdEncryptionKeyName"].(string); ok {
			// Get version from parameters or retrieve using Azure Key Vault client
			ver, hasVersion := parameters["etcdEncryptionKeyVersion"].(string)
			if !hasVersion || ver == "" {
				// Get Azure credentials from test context
				azureCredentials, err := azidentity.NewAzureCLICredential(nil)
				if err != nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed building development environment CLI credential: %w", err)
				}
				// Create Key Vault client to get key version
				keyVaultURL := fmt.Sprintf("https://%s.vault.azure.net/", kv)
				client, err := azkeys.NewClient(keyVaultURL, azureCredentials, nil)
				if err != nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to create key vault client: %w", err)
				}

				// Get key versions (sorted by creation date, latest first)
				versions := client.NewListKeyPropertiesVersionsPager(key, nil)
				page, err := versions.NextPage(ctx)
				if err != nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to list key versions: %w", err)
				}

				if len(page.Value) == 0 {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("no key versions found for key %s", key)
				}
				// Extract version from the key ID (last part of the URL path)
				if page.Value[0].KID != nil {
					keyID := *page.Value[0].KID
					parts := strings.Split(string(keyID), "/")
					if len(parts) > 0 {
						ver = parts[len(parts)-1]
					} else {
						return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to extract version from key ID: %s", keyID)
					}
				} else {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("key ID is nil for key %s", key)
				}
			}

			cluster.Properties.Etcd = &hcpsdk20240610preview.EtcdProfile{
				DataEncryption: &hcpsdk20240610preview.EtcdDataEncryptionProfile{
					KeyManagementMode: (*hcpsdk20240610preview.EtcdDataEncryptionKeyManagementModeType)(to.Ptr("CustomerManaged")),
					CustomerManaged: &hcpsdk20240610preview.CustomerManagedEncryptionProfile{
						EncryptionType: (*hcpsdk20240610preview.CustomerManagedEncryptionType)(to.Ptr("KMS")),
						Kms: &hcpsdk20240610preview.KmsEncryptionProfile{
							ActiveKey: &hcpsdk20240610preview.KmsKey{
								VaultName: &kv,
								Name:      &key,
								Version:   &ver,
							},
						},
					},
				},
			}
		}
	}

	// Top-level Managed Identity assignment from bicep output
	if idVal, ok := parameters["identityValue"]; ok {
		if b, err := json.Marshal(idVal); err == nil {
			var msi hcpsdk20240610preview.ManagedServiceIdentity
			if err := json.Unmarshal(b, &msi); err == nil {
				cluster.Identity = &msi
			}
		}
	}
	return cluster, nil
}

// BuildHCPClusterFromCompiledTemplate builds an HCP cluster object by reading defaults
// and constants from a compiled ARM template JSON and overlaying provided parameters.
// This keeps the dev path aligned with the bicep output used by tests.
func BuildHCPClusterFromCompiledTemplate(
	ctx context.Context,
	templateJSON []byte,
	parameters map[string]interface{},
	location string,
	subscriptionId string,
	resourceGroupName string,
	testContext *perItOrDescribeTestContext,
) (hcpsdk20240610preview.HcpOpenShiftCluster, error) {
	// Parse template
	tmplMap := map[string]interface{}{}
	if err := json.Unmarshal(templateJSON, &tmplMap); err != nil {
		return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to parse compiled template: %w", err)
	}

	// Helper to resolve parameter value or defaultValue from template
	resolveParam := func(name string) (interface{}, bool) {
		if v, ok := parameters[name]; ok {
			return v, true
		}
		if pmRaw, ok := tmplMap["parameters"]; ok {
			if pm, ok := pmRaw.(map[string]interface{}); ok {
				if pdefRaw, ok := pm[name]; ok {
					if pdef, ok := pdefRaw.(map[string]interface{}); ok {
						if dv, ok := pdef["defaultValue"]; ok {
							return dv, true
						}
					}
				}
			}
		}
		return nil, false
	}

	// Start with a blank cluster and fill in from params/defaults
	cluster := hcpsdk20240610preview.HcpOpenShiftCluster{
		Location:   &location,
		Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{},
	}
	cluster.Properties.Platform = &hcpsdk20240610preview.PlatformProfile{}

	// Version and channelGroup
	if vRaw, ok := resolveParam("openshiftVersionId"); ok {
		if v, ok := vRaw.(string); ok {
			cluster.Properties.Version = &hcpsdk20240610preview.VersionProfile{ID: &v}
		}
	}
	if cluster.Properties.Version == nil {
		cluster.Properties.Version = &hcpsdk20240610preview.VersionProfile{}
	}
	if cluster.Properties.Version.ChannelGroup == nil {
		cluster.Properties.Version.ChannelGroup = to.Ptr("stable")
	}

	// Managed RG
	if mrg, ok := parameters["managedResourceGroupName"].(string); ok {
		cluster.Properties.Platform.ManagedResourceGroup = &mrg
	}

	// NSG/Subnet/VNET to IDs
	if nsgName, ok := parameters["nsgName"].(string); ok {
		nsgID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s", subscriptionId, resourceGroupName, nsgName)
		cluster.Properties.Platform.NetworkSecurityGroupID = &nsgID
	}
	if subnetName, ok := parameters["subnetName"].(string); ok {
		if vnetName, ok := parameters["vnetName"].(string); ok {
			subnetID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s", subscriptionId, resourceGroupName, vnetName, subnetName)
			cluster.Properties.Platform.SubnetID = &subnetID
		}
	}

	// OperatorsAuthentication (UAMIs)
	if uamiVal, ok := parameters["userAssignedIdentitiesValue"]; ok {
		if b, err := json.Marshal(uamiVal); err == nil {
			var uamis hcpsdk20240610preview.UserAssignedIdentitiesProfile
			if err := json.Unmarshal(b, &uamis); err == nil {
				cluster.Properties.Platform.OperatorsAuthentication = &hcpsdk20240610preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: &uamis,
				}
			}
		}
	}

	// Network: prefer parameters[networkConfig], else defaultValue from template
	{
		netProfile := &hcpsdk20240610preview.NetworkProfile{}
		if raw, ok := resolveParam("networkConfig"); ok {
			if netCfg, ok := raw.(map[string]interface{}); ok {
				if v, ok := netCfg["networkType"].(string); ok && v != "" {
					netProfile.NetworkType = (*hcpsdk20240610preview.NetworkType)(to.Ptr(v))
				}
				if v, ok := netCfg["podCidr"].(string); ok && v != "" {
					netProfile.PodCIDR = &v
				}
				if v, ok := netCfg["serviceCidr"].(string); ok && v != "" {
					netProfile.ServiceCIDR = &v
				}
				if v, ok := netCfg["machineCidr"].(string); ok && v != "" {
					netProfile.MachineCIDR = &v
				}
				switch hv := netCfg["hostPrefix"].(type) {
				case int:
					vv := int32(hv)
					netProfile.HostPrefix = &vv
				case int32:
					vv := hv
					netProfile.HostPrefix = &vv
				case float64:
					vv := int32(hv)
					netProfile.HostPrefix = &vv
				}
			}
		}
		cluster.Properties.Network = netProfile
	}

	// Etcd KMS (resolve version via Key Vault if not provided directly)
	if kv, ok := parameters["keyVaultName"].(string); ok {
		if key, ok := parameters["etcdEncryptionKeyName"].(string); ok {
			ver, _ := parameters["etcdEncryptionKeyVersion"].(string)
			if ver == "" {
				azureCredentials, err := azidentity.NewAzureCLICredential(nil)
				if err != nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed building development environment CLI credential: %w", err)
				}
				client, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net/", kv), azureCredentials, nil)
				if err != nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to create key vault client: %w", err)
				}
				versions := client.NewListKeyPropertiesVersionsPager(key, nil)
				page, err := versions.NextPage(ctx)
				if err != nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("failed to list key versions: %w", err)
				}
				if len(page.Value) == 0 || page.Value[0].KID == nil {
					return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("no key versions found for key %s", key)
				}
				keyID := string(*page.Value[0].KID)
				parts := strings.Split(keyID, "/")
				ver = parts[len(parts)-1]
			}
			cluster.Properties.Etcd = &hcpsdk20240610preview.EtcdProfile{
				DataEncryption: &hcpsdk20240610preview.EtcdDataEncryptionProfile{
					KeyManagementMode: (*hcpsdk20240610preview.EtcdDataEncryptionKeyManagementModeType)(to.Ptr("CustomerManaged")),
					CustomerManaged: &hcpsdk20240610preview.CustomerManagedEncryptionProfile{
						EncryptionType: (*hcpsdk20240610preview.CustomerManagedEncryptionType)(to.Ptr("KMS")),
						Kms: &hcpsdk20240610preview.KmsEncryptionProfile{
							ActiveKey: &hcpsdk20240610preview.KmsKey{VaultName: &kv, Name: &key, Version: &ver},
						},
					},
				},
			}
		}
	}

	// Identity
	if idVal, ok := parameters["identityValue"]; ok {
		if b, err := json.Marshal(idVal); err == nil {
			var msi hcpsdk20240610preview.ManagedServiceIdentity
			if err := json.Unmarshal(b, &msi); err == nil {
				cluster.Identity = &msi
			}
		}
	}

	// Read API visibility and image registry state constants from template if provided
	if resRaw, ok := tmplMap["resources"].([]interface{}); ok {
		for _, r := range resRaw {
			if rmap, ok := r.(map[string]interface{}); ok {
				if t, _ := rmap["type"].(string); t == "Microsoft.RedHatOpenShift/hcpOpenShiftClusters" {
					if props, ok := rmap["properties"].(map[string]interface{}); ok {
						if api, ok := props["api"].(map[string]interface{}); ok {
							if vis, ok := api["visibility"].(string); ok && vis != "" {
								cluster.Properties.API = &hcpsdk20240610preview.APIProfile{Visibility: (*hcpsdk20240610preview.Visibility)(to.Ptr(vis))}
							}
						}
						if reg, ok := props["clusterImageRegistry"].(map[string]interface{}); ok {
							if st, ok := reg["state"].(string); ok && st != "" {
								cluster.Properties.ClusterImageRegistry = &hcpsdk20240610preview.ClusterImageRegistryProfile{State: (*hcpsdk20240610preview.ClusterImageRegistryProfileState)(to.Ptr(st))}
							}
						}
					}
					break
				}
			}
		}
	}

	return cluster, nil
}

// CreateNodePoolAndWait creates a NodePool via direct API call and waits for completion, this is to support lower environments.
func CreateNodePoolAndWait(
	ctx context.Context,
	nodePoolsClient *hcpsdk20240610preview.NodePoolsClient,
	resourceGroupName string,
	hcpClusterName string,
	nodePoolName string,
	nodePool hcpsdk20240610preview.NodePool,
	timeout time.Duration,
) (*hcpsdk20240610preview.NodePool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
		return &m.NodePool, nil
	default:
		fmt.Printf("unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// BuildNodePoolFromBicepTemplate converts bicep template and parameters to a NodePool object
func BuildNodePoolFromBicepTemplate(
	ctx context.Context,
	parameters map[string]interface{},
	location string,
	subscriptionId string,
	resourceGroupName string,
) (hcpsdk20240610preview.NodePool, error) {
	// Create NodePool struct directly from parameters
	nodePool := hcpsdk20240610preview.NodePool{
		Location:   &location,
		Properties: &hcpsdk20240610preview.NodePoolProperties{},
	}
	// Set required Platform profile
	nodePool.Properties.Platform = &hcpsdk20240610preview.NodePoolPlatformProfile{}
	// Map bicep parameters to nodepool properties
	if openshiftVersionId, ok := parameters["openshiftVersionId"].(string); ok {
		nodePool.Properties.Version = &hcpsdk20240610preview.NodePoolVersionProfile{
			ID: &openshiftVersionId,
		}
	}
	// Handle replicas - support both int and float64 from JSON
	if replicas, ok := parameters["replicas"]; ok {
		switch v := replicas.(type) {
		case int:
			replicasInt32 := int32(v)
			nodePool.Properties.Replicas = &replicasInt32
		case int32:
			nodePool.Properties.Replicas = &v
		}
	}
	// Set VM size - default if not specified
	if vmSize, ok := parameters["vmSize"].(string); ok {
		nodePool.Properties.Platform.VMSize = &vmSize
	} else {
		// Default VM size for nodepools
		defaultVMSize := "Standard_D8s_v3"
		nodePool.Properties.Platform.VMSize = &defaultVMSize
	}

	return nodePool, nil
}

// BuildNodePoolFromCompiledTemplate builds a NodePool object from the compiled nodepool template JSON
// and the provided parameters, keeping the dev path aligned with test artifacts.
func BuildNodePoolFromCompiledTemplate(
	ctx context.Context,
	templateJSON []byte,
	parameters map[string]interface{},
	location string,
	subscriptionId string,
	resourceGroupName string,
) (hcpsdk20240610preview.NodePool, error) {
	tmplMap := map[string]interface{}{}
	if err := json.Unmarshal(templateJSON, &tmplMap); err != nil {
		return hcpsdk20240610preview.NodePool{}, fmt.Errorf("failed to parse compiled nodepool template: %w", err)
	}

	resolveParam := func(name string) (interface{}, bool) {
		if v, ok := parameters[name]; ok {
			return v, true
		}
		if pmRaw, ok := tmplMap["parameters"]; ok {
			if pm, ok := pmRaw.(map[string]interface{}); ok {
				if pdefRaw, ok := pm[name]; ok {
					if pdef, ok := pdefRaw.(map[string]interface{}); ok {
						if dv, ok := pdef["defaultValue"]; ok {
							return dv, true
						}
					}
				}
			}
		}
		return nil, false
	}

	nodePool := hcpsdk20240610preview.NodePool{
		Location:   &location,
		Properties: &hcpsdk20240610preview.NodePoolProperties{},
	}
	nodePool.Properties.Platform = &hcpsdk20240610preview.NodePoolPlatformProfile{}

	// Locate nodepool resource properties in template (for dynamic constant detection)
	var nodePoolProps map[string]interface{}
	if resRaw, ok := tmplMap["resources"].([]interface{}); ok {
		for _, r := range resRaw {
			if rmap, ok := r.(map[string]interface{}); ok {
				if t, _ := rmap["type"].(string); t == "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools" {
					if props, ok := rmap["properties"].(map[string]interface{}); ok {
						nodePoolProps = props
					}
					break
				}
			}
		}
	}

	// Version
	if vRaw, ok := resolveParam("openshiftVersionId"); ok {
		if v, ok := vRaw.(string); ok {
			nodePool.Properties.Version = &hcpsdk20240610preview.NodePoolVersionProfile{ID: &v}
		}
	}
	if nodePool.Properties.Version == nil {
		nodePool.Properties.Version = &hcpsdk20240610preview.NodePoolVersionProfile{}
	}
	if nodePool.Properties.Version.ChannelGroup == nil {
		nodePool.Properties.Version.ChannelGroup = to.Ptr("stable")
	}

	// Replicas
	if replicas, ok := parameters["replicas"]; ok {
		switch v := replicas.(type) {
		case int:
			vv := int32(v)
			nodePool.Properties.Replicas = &vv
		case int32:
			nodePool.Properties.Replicas = &v
		}
	}

	// VM Size
	if vmSize, ok := parameters["vmSize"].(string); ok && vmSize != "" {
		nodePool.Properties.Platform.VMSize = &vmSize
	} else if vRaw, ok := resolveParam("vmSize"); ok {
		if v, ok := vRaw.(string); ok && v != "" {
			nodePool.Properties.Platform.VMSize = &v
		}
	}

	// OSDisk
	if nodePool.Properties.Platform.OSDisk == nil {
		nodePool.Properties.Platform.OSDisk = &hcpsdk20240610preview.OsDiskProfile{}
	}
	// sizeGiB
	if size, ok := parameters["osDiskSizeGiB"]; ok {
		switch s := size.(type) {
		case int:
			vv := int32(s)
			nodePool.Properties.Platform.OSDisk.SizeGiB = &vv
		case int32:
			nodePool.Properties.Platform.OSDisk.SizeGiB = &s
		case float64:
			vv := int32(s)
			nodePool.Properties.Platform.OSDisk.SizeGiB = &vv
		}
	} else if vRaw, ok := resolveParam("osDiskSizeGiB"); ok {
		switch s := vRaw.(type) {
		case int:
			vv := int32(s)
			nodePool.Properties.Platform.OSDisk.SizeGiB = &vv
		case int32:
			nodePool.Properties.Platform.OSDisk.SizeGiB = &s
		case float64:
			vv := int32(s)
			nodePool.Properties.Platform.OSDisk.SizeGiB = &vv
		}
	}
	// diskStorageAccountType: read constant from compiled template if present
	if nodePool.Properties.Platform.OSDisk.DiskStorageAccountType == nil {
		if nodePoolProps != nil {
			if plat, ok := nodePoolProps["platform"].(map[string]interface{}); ok {
				if osd, ok := plat["osDisk"].(map[string]interface{}); ok {
					if t, ok := osd["diskStorageAccountType"].(string); ok && t != "" {
						nodePool.Properties.Platform.OSDisk.DiskStorageAccountType = (*hcpsdk20240610preview.DiskStorageAccountType)(to.Ptr(t))
					}
				}
			}
		}
	}

	return nodePool, nil
}
