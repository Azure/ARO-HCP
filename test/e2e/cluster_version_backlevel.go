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

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	backlevelVersions := []backlevelVersionSpec{
		{
			controlPlaneVersion: "4.19",
			nodePoolVersions:    []string{"4.19.7"},
			bicepModulesDir:     "test-artifacts/generated-test-artifacts/modules-4.19",
		},
	}

	for _, version := range backlevelVersions {
		It("should be able to create an HCP cluster with back-level version "+version.controlPlaneVersion,
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			func(ctx context.Context) {
				const (
					customerNetworkSecurityGroupName = "customer-nsg-name-"
					customerVnetName                 = "customer-vnet-name-"
					customerVnetSubnetName           = "customer-vnet-subnet-"
					customerClusterName              = "cluster-ver-"
					customerNodePoolName             = "np-ver-"
				)
				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
					Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-back-version", tc.Location())
				Expect(err).NotTo(HaveOccurred(), "failed to create resource group for back-level version test")

				clusterSuffix := strings.ReplaceAll(version.controlPlaneVersion, ".", "-")
				clusterName := customerClusterName + clusterSuffix
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+clusterSuffix, "-managed", 64)

				By("creating customer infrastructure")
				customerInfraDeploymentName := fmt.Sprintf("customer-infra-%s-%s", clusterName, rand.String(6))
				customerInfraDeployment, err := tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, version.bicepModulesDir+"/customer-infra.json"),
					framework.WithDeploymentName(customerInfraDeploymentName),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]any{
						"customerNsgName":        customerNetworkSecurityGroupName + clusterSuffix,
						"customerVnetName":       customerVnetName + clusterSuffix,
						"customerVnetSubnetName": customerVnetSubnetName + clusterSuffix,
					}),
					framework.WithTimeout(45*time.Minute),
				)
				Expect(err).NotTo(HaveOccurred(), "failed to deploy customer infrastructure for back-level version %s", version.controlPlaneVersion)

				customerInfraOutputs, err := readCustomerInfraOutputs(customerInfraDeployment)
				Expect(err).NotTo(HaveOccurred(), "failed to read customer infrastructure outputs")

				By("creating managed identities")
				identityPool, usePooledIdentities, err := tc.ResolveIdentitiesForTemplate(*resourceGroup.Name)
				Expect(err).NotTo(HaveOccurred(), "failed to resolve identities for managed identities template")
				managedIdentitiesDeploymentName := fmt.Sprintf("mi-%s-%s", clusterName, rand.String(6))
				managedIdentitiesDeployment, err := tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, version.bicepModulesDir+"/managed-identities.json"),
					framework.WithDeploymentName(managedIdentitiesDeploymentName),
					framework.WithScope(framework.BicepDeploymentScopeSubscription),
					framework.WithLocation(tc.Location()),
					framework.WithParameters(map[string]any{
						"nsgName":                  customerInfraOutputs.nsgName,
						"vnetName":                 customerInfraOutputs.vnetName,
						"subnetName":               customerInfraOutputs.subnetName,
						"keyVaultName":             customerInfraOutputs.keyVaultName,
						"useMsiPool":               usePooledIdentities,
						"clusterResourceGroupName": *resourceGroup.Name,
						"msiResourceGroupName":     identityPool.ResourceGroupName,
						"identities":               identityPool.Identities,
					}),
					framework.WithTimeout(45*time.Minute),
				)
				Expect(err).NotTo(HaveOccurred(), "failed to deploy managed identities for back-level version %s", version.controlPlaneVersion)
				userAssignedIdentitiesValue, err := framework.GetOutputValue(managedIdentitiesDeployment, "userAssignedIdentitiesValue")
				Expect(err).NotTo(HaveOccurred(), "failed to get userAssignedIdentitiesValue output from managed identities deployment")
				identityValue, err := framework.GetOutputValue(managedIdentitiesDeployment, "identityValue")
				Expect(err).NotTo(HaveOccurred(), "failed to get identityValue output from managed identities deployment")
				userAssignedIdentitiesProfile, err := framework.ConvertToUserAssignedIdentitiesProfile20240610(userAssignedIdentitiesValue)
				Expect(err).NotTo(HaveOccurred(), "failed to convert userAssignedIdentitiesValue to profile")
				identityProfile, err := framework.ConvertToManagedServiceIdentity20240610(identityValue)
				Expect(err).NotTo(HaveOccurred(), "failed to convert identityValue to ManagedServiceIdentity")

				By("creating HCP cluster version " + version.controlPlaneVersion)
				cluster, err := buildHCPClusterRequest(
					tc.Location(),
					managedResourceGroupName,
					version.controlPlaneVersion,
					defaultChannelGroup,
					customerInfraOutputs,
					userAssignedIdentitiesProfile,
					identityProfile,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster request for version %s", version.controlPlaneVersion)
				hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
				_, err = framework.CreateHCPClusterAndWait20240610(
					ctx,
					GinkgoLogr,
					hcpClient,
					*resourceGroup.Name,
					clusterName,
					cluster,
					framework.ClusterCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster version %s", version.controlPlaneVersion)

				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					framework.GetAdminRESTConfigTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for back-level cluster version %s", version.controlPlaneVersion)

				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability for back-level version %s", version.controlPlaneVersion)

				By("creating node pool with back-level version")
				var matchingNodePoolVersion string
				for _, nodePoolVersion := range version.nodePoolVersions {
					if strings.HasPrefix(nodePoolVersion, version.controlPlaneVersion+".") {
						matchingNodePoolVersion = nodePoolVersion
						break
					}
				}

				if matchingNodePoolVersion != "" {
					nodePoolSuffix := strings.ReplaceAll(matchingNodePoolVersion, ".", "-")
					nodePoolName := customerNodePoolName + nodePoolSuffix

					By("creating node pool version " + matchingNodePoolVersion + " and verifying a simple web app can run")
					nodePoolDefaults := defaultNodePoolDefaults
					nodePoolDefaults.vmSize, err = tc.SelectVMSize(ctx, framework.DefaultWorkerVMSizeSelector())
					Expect(err).NotTo(HaveOccurred(), "failed to resolve the default worker VM size for the back-level node pool; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())
					nodePool, err := buildNodePoolRequest(
						tc.Location(),
						matchingNodePoolVersion,
						nodePoolDefaults,
					)
					Expect(err).NotTo(HaveOccurred(), "failed to build node pool request for version %s", matchingNodePoolVersion)
					nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
					_, err = framework.CreateNodePoolAndWait20240610(ctx,
						nodePoolClient,
						*resourceGroup.Name,
						clusterName,
						nodePoolName,
						nodePool,
						framework.NodePoolCreationTimeout,
					)
					Expect(err).NotTo(HaveOccurred(), "failed to create node pool version %s for back-level cluster", matchingNodePoolVersion)

					err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
					Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app on back-level cluster version %s", version.controlPlaneVersion)
				}

			})
	}
})

type backlevelVersionSpec struct {
	controlPlaneVersion string
	nodePoolVersions    []string
	bicepModulesDir     string
}

type nodePoolDefaults struct {
	replicas               int32
	vmSize                 string
	osDiskSizeGiB          int32
	diskStorageAccountType string
	channelGroup           string
}

var defaultNodePoolDefaults = nodePoolDefaults{
	replicas: int32(2),
	// vmSize is intentionally left empty: it is resolved at create time via the
	// restriction-aware framework.DefaultWorkerVMSizeSelector so the suite is
	// resilient to per-subscription SKU restrictions.
	vmSize:                 "",
	osDiskSizeGiB:          int32(64),
	diskStorageAccountType: framework.DefaultDiskStorageAccountType,
	channelGroup:           "stable",
}

const defaultChannelGroup = "stable"

type customerInfraOutputs struct {
	keyVaultName             string
	etcdEncryptionKeyName    string
	etcdEncryptionKeyVersion string
	nsgID                    string
	subnetID                 string
	vnetName                 string
	nsgName                  string
	subnetName               string
}

func buildHCPClusterRequest(
	location string,
	managedResourceGroupName string,
	controlPlaneVersion string,
	channelGroup string,
	customerInfra customerInfraOutputs,
	userAssignedIdentitiesProfile *hcpsdk20240610preview.UserAssignedIdentitiesProfile,
	identityProfile *hcpsdk20240610preview.ManagedServiceIdentity,
) (hcpsdk20240610preview.HcpOpenShiftCluster, error) {

	switch controlPlaneVersion {
	case "4.19":
		return buildHCPClusterRequest_4_19(location, managedResourceGroupName, controlPlaneVersion, channelGroup, customerInfra, userAssignedIdentitiesProfile, identityProfile), nil
	default:
		return hcpsdk20240610preview.HcpOpenShiftCluster{}, fmt.Errorf("unsupported control plane version: %s", controlPlaneVersion)
	}
}

func buildHCPClusterRequest_4_19(
	location string,
	managedResourceGroupName string,
	controlPlaneVersion string,
	channelGroup string,
	customerInfra customerInfraOutputs,
	userAssignedIdentitiesProfile *hcpsdk20240610preview.UserAssignedIdentitiesProfile,
	identityProfile *hcpsdk20240610preview.ManagedServiceIdentity,
) hcpsdk20240610preview.HcpOpenShiftCluster {
	return hcpsdk20240610preview.HcpOpenShiftCluster{
		Location: to.Ptr(location),
		Identity: identityProfile,
		Tags: map[string]*string{
			api.TagClusterSizeOverride: to.Ptr(string(api.MinimalControlPlanePodSizing)),
		},
		Properties: &hcpsdk20240610preview.HcpOpenShiftClusterProperties{
			Version: &hcpsdk20240610preview.VersionProfile{
				ID:           to.Ptr(controlPlaneVersion),
				ChannelGroup: to.Ptr(channelGroup),
			},
			Platform: &hcpsdk20240610preview.PlatformProfile{
				ManagedResourceGroup:   to.Ptr(managedResourceGroupName),
				NetworkSecurityGroupID: to.Ptr(customerInfra.nsgID),
				SubnetID:               to.Ptr(customerInfra.subnetID),
				OperatorsAuthentication: &hcpsdk20240610preview.OperatorsAuthenticationProfile{
					UserAssignedIdentities: userAssignedIdentitiesProfile,
				},
			},
			Network: &hcpsdk20240610preview.NetworkProfile{
				NetworkType: to.Ptr(hcpsdk20240610preview.NetworkType("OVNKubernetes")),
				PodCIDR:     to.Ptr("10.128.0.0/14"),
				ServiceCIDR: to.Ptr("172.30.0.0/16"),
				MachineCIDR: to.Ptr("10.0.0.0/16"),
				HostPrefix:  to.Ptr(int32(23)),
			},
			API: &hcpsdk20240610preview.APIProfile{
				Visibility: to.Ptr(hcpsdk20240610preview.Visibility("Public")),
			},
			ClusterImageRegistry: &hcpsdk20240610preview.ClusterImageRegistryProfile{
				State: to.Ptr(hcpsdk20240610preview.ClusterImageRegistryState("Enabled")),
			},
			Etcd: &hcpsdk20240610preview.EtcdProfile{
				DataEncryption: &hcpsdk20240610preview.EtcdDataEncryptionProfile{
					KeyManagementMode: to.Ptr(hcpsdk20240610preview.EtcdDataEncryptionKeyManagementModeType("CustomerManaged")),
					CustomerManaged: &hcpsdk20240610preview.CustomerManagedEncryptionProfile{
						EncryptionType: to.Ptr(hcpsdk20240610preview.CustomerManagedEncryptionType("KMS")),
						Kms: &hcpsdk20240610preview.KmsEncryptionProfile{
							ActiveKey: &hcpsdk20240610preview.KmsKey{
								VaultName: to.Ptr(customerInfra.keyVaultName),
								Name:      to.Ptr(customerInfra.etcdEncryptionKeyName),
								Version:   to.Ptr(customerInfra.etcdEncryptionKeyVersion),
							},
						},
					},
				},
			},
		},
	}
}

func buildNodePoolRequest(
	location string,
	nodePoolVersion string,
	defaults nodePoolDefaults,
) (hcpsdk20240610preview.NodePool, error) {
	switch nodePoolVersion {
	case "4.19.7":
		return buildNodePoolRequest_4_19(location, nodePoolVersion, defaults), nil
	default:
		return hcpsdk20240610preview.NodePool{}, fmt.Errorf("unsupported node pool version: %s", nodePoolVersion)
	}
}

func buildNodePoolRequest_4_19(
	location string,
	nodePoolVersion string,
	defaults nodePoolDefaults,
) hcpsdk20240610preview.NodePool {
	return hcpsdk20240610preview.NodePool{
		Location: to.Ptr(location),
		Properties: &hcpsdk20240610preview.NodePoolProperties{
			Version: &hcpsdk20240610preview.NodePoolVersionProfile{
				ID:           to.Ptr(nodePoolVersion),
				ChannelGroup: to.Ptr(defaults.channelGroup),
			},
			Replicas: to.Ptr(defaults.replicas),
			Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
				VMSize: to.Ptr(defaults.vmSize),
				OSDisk: &hcpsdk20240610preview.OsDiskProfile{
					SizeGiB:                to.Ptr(defaults.osDiskSizeGiB),
					DiskStorageAccountType: to.Ptr(hcpsdk20240610preview.DiskStorageAccountType(defaults.diskStorageAccountType)),
				},
			},
		},
	}
}

func readCustomerInfraOutputs(deployment *armresources.DeploymentExtended) (customerInfraOutputs, error) {
	keyVaultName, err := framework.GetOutputValueString(deployment, "keyVaultName")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get keyVaultName: %w", err)
	}
	etcdEncryptionKeyName, err := framework.GetOutputValueString(deployment, "etcdEncryptionKeyName")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get etcdEncryptionKeyName: %w", err)
	}
	etcdEncryptionKeyVersion, err := framework.GetOutputValueString(deployment, "etcdEncryptionKeyVersion")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get etcdEncryptionKeyVersion: %w", err)
	}
	nsgID, err := framework.GetOutputValueString(deployment, "nsgID")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get nsgID: %w", err)
	}
	subnetID, err := framework.GetOutputValueString(deployment, "vnetSubnetID")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get vnetSubnetID: %w", err)
	}
	vnetName, err := framework.GetOutputValueString(deployment, "vnetName")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get vnetName: %w", err)
	}
	nsgName, err := framework.GetOutputValueString(deployment, "nsgName")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get nsgName: %w", err)
	}
	subnetName, err := framework.GetOutputValueString(deployment, "vnetSubnetName")
	if err != nil {
		return customerInfraOutputs{}, fmt.Errorf("failed to get vnetSubnetName: %w", err)
	}

	return customerInfraOutputs{
		keyVaultName:             keyVaultName,
		etcdEncryptionKeyName:    etcdEncryptionKeyName,
		etcdEncryptionKeyVersion: etcdEncryptionKeyVersion,
		nsgID:                    nsgID,
		subnetID:                 subnetID,
		vnetName:                 vnetName,
		nsgName:                  nsgName,
		subnetName:               subnetName,
	}, nil
}
