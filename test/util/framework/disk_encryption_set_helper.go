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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

const (
	desKeyName = "des-encryption-key"

	kvCryptoServiceEncryptionUserRoleID = "e147488a-f6f5-4113-8e2d-b22465e65bf6"
	readerRoleID                        = "acdd72a7-3385-48ef-bd42-f606fba81ae7"
)

func (tc *perItOrDescribeTestContext) CreateDiskEncryptionSet(ctx context.Context, resourceGroupName, keyVaultName, clusterName, location string, readerPrincipalIDs ...string) (string, error) {
	startTime := time.Now()
	defer func() {
		tc.RecordTestStep("Create disk encryption set", startTime, time.Now())
	}()

	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get subscription ID: %w", err)
	}

	creds, err := tc.AzureCredential()
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	keyVaultURL := fmt.Sprintf("https://%s.vault.azure.net/", keyVaultName)
	keyVaultResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s", subscriptionID, resourceGroupName, keyVaultName)

	keyClient, err := azkeys.NewClient(keyVaultURL, creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Key Vault keys client: %w", err)
	}

	createKeyResp, err := keyClient.CreateKey(ctx, desKeyName, azkeys.CreateKeyParameters{
		Kty:     to.Ptr(azkeys.KeyTypeRSA),
		KeySize: to.Ptr(int32(2048)),
	}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create DES encryption key: %w", err)
	}
	if createKeyResp.Key == nil || createKeyResp.Key.KID == nil {
		return "", fmt.Errorf("created key response or KID was nil")
	}

	keyURL := string(*createKeyResp.Key.KID)
	ginkgo.GinkgoLogr.Info("Created DES encryption key", "keyVaultName", keyVaultName, "keyName", desKeyName, "keyURL", keyURL)

	desName := fmt.Sprintf("%s-des", clusterName)
	desClient := tc.GetARMComputeClientFactoryOrDie(ctx).NewDiskEncryptionSetsClient()

	poller, err := desClient.BeginCreateOrUpdate(ctx, resourceGroupName, desName, armcompute.DiskEncryptionSet{
		Location: &location,
		Identity: &armcompute.EncryptionSetIdentity{
			Type: to.Ptr(armcompute.DiskEncryptionSetIdentityTypeSystemAssigned),
		},
		Properties: &armcompute.EncryptionSetProperties{
			ActiveKey: &armcompute.KeyForDiskEncryptionSet{
				KeyURL: &keyURL,
				SourceVault: &armcompute.SourceVault{
					ID: &keyVaultResourceID,
				},
			},
			EncryptionType: to.Ptr(armcompute.DiskEncryptionSetTypeEncryptionAtRestWithCustomerKey),
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to begin creating disk encryption set: %w", err)
	}

	desResult, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create disk encryption set: %w", err)
	}

	desResourceID := *desResult.ID
	ginkgo.GinkgoLogr.Info("Created disk encryption set", "name", desName, "resourceID", desResourceID)

	if desResult.Identity == nil || desResult.Identity.PrincipalID == nil {
		return "", fmt.Errorf("disk encryption set has no system-assigned identity principal ID")
	}

	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create role assignments client: %w", err)
	}

	kvCryptoRoleDefID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, kvCryptoServiceEncryptionUserRoleID)
	kvRoleAssignmentName := guid(keyVaultResourceID, *desResult.Identity.PrincipalID, kvCryptoRoleDefID)

	kvRoleResult, err := roleAssignmentsClient.Create(ctx, keyVaultResourceID, kvRoleAssignmentName, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      desResult.Identity.PrincipalID,
			RoleDefinitionID: &kvCryptoRoleDefID,
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to assign Key Vault Crypto Service Encryption User to DES identity: %w", err)
	}
	tc.trackRoleAssignment(*kvRoleResult.ID)
	ginkgo.GinkgoLogr.Info("Assigned KV Crypto Service Encryption User to DES identity", "scope", keyVaultResourceID, "principalID", *desResult.Identity.PrincipalID)

	readerRoleDefID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, readerRoleID)
	for _, principalID := range readerPrincipalIDs {
		if principalID == "" {
			continue
		}
		pid := principalID
		desReaderAssignmentName := guid(desResourceID, pid, readerRoleDefID)
		desReaderResult, err := roleAssignmentsClient.Create(ctx, desResourceID, desReaderAssignmentName, armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				PrincipalID:      &pid,
				RoleDefinitionID: &readerRoleDefID,
				PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
			},
		}, nil)
		if err != nil {
			return "", fmt.Errorf("failed to assign Reader to principal %s on DES: %w", pid, err)
		}
		tc.trackRoleAssignment(*desReaderResult.ID)
		ginkgo.GinkgoLogr.Info("Assigned Reader to principal on DES", "scope", desResourceID, "principalID", pid)
	}

	return desResourceID, nil
}
