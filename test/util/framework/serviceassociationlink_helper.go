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
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/certificate"
	"github.com/Azure/ARO-HCP/internal/fpa"
)

const (
	redHatOpenShiftSALName           = "RedHatOpenShift"
	redHatOpenShiftLinkedResource    = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
	serviceAssociationLinkAPIVersion = "2021-08-01"
)

func (tc *perItOrDescribeTestContext) deleteRedHatOpenShiftServiceAssociationLinks(
	ctx context.Context,
	resourceGroupName string,
	creds FPACredentials,
) error {
	networkClientFactory, err := tc.GetARMNetworkClientFactory(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ARM network client factory: %w", err)
	}

	fpaResourcesClient, err := tc.newFPAResourcesClient(ctx, creds)
	if err != nil {
		return fmt.Errorf("failed to create FPA resources client: %w", err)
	}

	return deleteRedHatOpenShiftSALs(ctx, networkClientFactory, fpaResourcesClient, resourceGroupName)
}

func (tc *perItOrDescribeTestContext) newFPAResourcesClient(
	ctx context.Context,
	creds FPACredentials,
) (*armresources.Client, error) {
	certReader := certificate.NewAzureIdentityFileReader(creds.CertPath)
	clientOptions := tc.perBinaryInvocationTestContext.getClientFactoryOptions()

	credentialRetriever, err := fpa.NewFirstPartyApplicationTokenCredentialRetriever(
		creds.ClientID,
		certReader,
		clientOptions.ClientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FPA credential retriever: %w", err)
	}

	credential, err := credentialRetriever.RetrieveCredential(tc.TenantID())
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve FPA credential: %w", err)
	}

	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription ID: %w", err)
	}

	clientFactory, err := armresources.NewClientFactory(
		subscriptionID,
		credential,
		clientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create FPA ARM resources client factory: %w", err)
	}

	return clientFactory.NewClient(), nil
}

func deleteRedHatOpenShiftSALs(
	ctx context.Context,
	networkClientFactory *armnetwork.ClientFactory,
	fpaResourcesClient *armresources.Client,
	resourceGroupName string,
) error {
	vnetClient := networkClientFactory.NewVirtualNetworksClient()
	subnetClient := networkClientFactory.NewSubnetsClient()

	vnetPager := vnetClient.NewListPager(resourceGroupName, nil)
	for vnetPager.More() {
		vnetPage, err := vnetPager.NextPage(ctx)
		if err != nil {
			if isResourceGroupNotFoundError(err) {
				return nil
			}
			return fmt.Errorf("failed listing VNets in resource group %q: %w", resourceGroupName, err)
		}

		for _, vnet := range vnetPage.Value {
			if vnet == nil || vnet.Name == nil {
				continue
			}

			subnetsPager := subnetClient.NewListPager(resourceGroupName, *vnet.Name, nil)
			for subnetsPager.More() {
				subnetPage, err := subnetsPager.NextPage(ctx)
				if err != nil {
					if isResourceGroupNotFoundError(err) {
						return nil
					}
					return fmt.Errorf("failed listing subnets in resource group %q VNet %q: %w", resourceGroupName, *vnet.Name, err)
				}

				for _, subnet := range subnetPage.Value {
					if subnet == nil || subnet.ID == nil || subnet.Name == nil || subnet.Properties == nil {
						continue
					}

					for _, sal := range subnet.Properties.ServiceAssociationLinks {
						if sal == nil || sal.ID == nil {
							continue
						}

						if !isRedHatOpenShiftServiceAssociationLink(sal) {
							continue
						}

						ginkgo.GinkgoLogr.Info(
							"deleting RedHatOpenShift service association link",
							"resourceGroup", resourceGroupName,
							"vnet", *vnet.Name,
							"subnet", *subnet.Name,
							"serviceAssociationLink", *sal.ID,
						)

						if err := deleteResourceByID(ctx, fpaResourcesClient, *sal.ID, serviceAssociationLinkAPIVersion); err != nil {
							return fmt.Errorf("failed deleting service association link %q: %w", *sal.ID, err)
						}
					}
				}
			}
		}
	}

	return nil
}

func isRedHatOpenShiftServiceAssociationLink(sal *armnetwork.ServiceAssociationLink) bool {
	if sal == nil {
		return false
	}

	if sal.Name != nil && strings.EqualFold(*sal.Name, redHatOpenShiftSALName) {
		return true
	}

	if sal.Properties != nil &&
		sal.Properties.LinkedResourceType != nil &&
		strings.EqualFold(*sal.Properties.LinkedResourceType, redHatOpenShiftLinkedResource) {
		return true
	}

	return false
}

func isResourceNotFoundError(err error) bool {
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		if responseErr.StatusCode == http.StatusNotFound {
			return true
		}
		if responseErr.ErrorCode == "ResourceNotFound" {
			return true
		}
	}
	return false
}

func deleteResourceByID(
	ctx context.Context,
	client *armresources.Client,
	resourceID string,
	apiVersion string,
) error {
	poller, err := client.BeginDeleteByID(ctx, resourceID, apiVersion, nil)
	if err != nil {
		// If the resource is already deleted (404), treat as success
		if isResourceNotFoundError(err) {
			return nil
		}
		return err
	}

	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil && isResourceNotFoundError(err) {
		// Resource was deleted during polling
		return nil
	}
	return err
}
