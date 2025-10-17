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
	if isDevelopmentEnvironment() {
		ret.TLSClientConfig.Insecure = true
		ret.TLSClientConfig.CAData = nil
		ret.TLSClientConfig.CAFile = ""
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

	fmt.Printf("DEBUG: Creating HCP cluster %s via direct API call\n", hcpClusterName)

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
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

// BuildHCPClusterFromBicepTemplate converts bicep template and parameters to an HCP cluster object
func BuildHCPClusterFromBicepTemplate(
	ctx context.Context,
	bicepTemplateJSON []byte,
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
		// SystemData is read-only and should not be set in requests
	}

	// Set required Platform profile
	cluster.Properties.Platform = &hcpsdk20240610preview.PlatformProfile{}

	// Map bicep parameters to cluster properties
	if openshiftVersionId, ok := parameters["openshiftVersionId"].(string); ok {
		cluster.Properties.Version = &hcpsdk20240610preview.VersionProfile{
			ID: &openshiftVersionId,
		}
		// Ensure default channel group mirrors bicep default when not explicitly provided
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
		// Safest: re-marshal then unmarshal into the exact SDK type
		if b, err := json.Marshal(uamiVal); err == nil {
			var uamis hcpsdk20240610preview.UserAssignedIdentitiesProfile
			if err := json.Unmarshal(b, &uamis); err == nil {
				cluster.Properties.Platform.OperatorsAuthentication = &hcpsdk20240610preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: &uamis,
				}
			}
		}
	}

	// ETCD encryption is required - platform managed is not supported
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

	fmt.Printf("DEBUG: Successfully built HCP cluster struct from parameters\n")
	return cluster, nil
}
