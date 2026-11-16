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

package validationutils

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
)

const (
	diskEncryptionSetReadAction = "Microsoft.Compute/diskEncryptionSets/read"

	// AzureNodePoolDiskEncryptionSetPermissionsValidationName is the validation's name, used as the
	// condition Type on ServiceProviderNodePool.Status.Validations and to scope the create-operation gate.
	AzureNodePoolDiskEncryptionSetPermissionsValidationName = "AzureNodePoolDiskEncryptionSetPermissionsValidation"
)

type AzureNodePoolDiskEncryptionSetPermissionsValidation struct {
	checkAccessV2ClientBuilder                  azureclient.CheckAccessV2ClientBuilder
	miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	checkAccessV2Scope                          string
}

func NewAzureNodePoolDiskEncryptionSetPermissionsValidation(checkAccessV2ClientBuilder azureclient.CheckAccessV2ClientBuilder, miDataplaneBasedAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder, checkAccessV2Scope string) *AzureNodePoolDiskEncryptionSetPermissionsValidation {
	return &AzureNodePoolDiskEncryptionSetPermissionsValidation{
		checkAccessV2ClientBuilder:                  checkAccessV2ClientBuilder,
		miDataplaneBasedAccessTokenRetrieverBuilder: miDataplaneBasedAccessTokenRetrieverBuilder,
		checkAccessV2Scope:                          checkAccessV2Scope,
	}
}

func (v *AzureNodePoolDiskEncryptionSetPermissionsValidation) Name() string {
	return AzureNodePoolDiskEncryptionSetPermissionsValidationName
}

var _ NodePoolValidation = (*AzureNodePoolDiskEncryptionSetPermissionsValidation)(nil)

func (v *AzureNodePoolDiskEncryptionSetPermissionsValidation) Validate(ctx context.Context, cluster *coreapi.Cluster, nodePoolSubscription *coreapi.Subscription, nodePool *coreapi.NodePool) ValidationResult {
	desID := nodePool.Properties.Platform.OSDisk.EncryptionSetID
	if desID == nil {
		return SkippedValidation(
			"NotApplicable",
			"No OS disk encryption set configured.",
			"nodePool OSDisk.EncryptionSetID is nil, skipping validation",
		)
	}

	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	smiResourceID := identities.ServiceManagedIdentity

	capzName := string(internalazure.ClusterOperatorIdentifierClusterAPIAzure)
	capzResourceID, ok := identities.ControlPlaneOperators[capzName]
	if !ok || capzResourceID == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("CAPZ identity (%s) not found in cluster control plane operator identities", capzName),
			ControllerReportingPolicyTypeError,
		)
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			"node pool subscription has no tenant ID",
			ControllerReportingPolicyTypeError,
		)
	}

	checkAccessClient, err := v.checkAccessV2ClientBuilder.Build(*nodePoolSubscription.Properties.TenantId)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("failed to build CheckAccess V2 client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL

	var missing []string
	for _, identity := range []*azcorearm.ResourceID{smiResourceID, capzResourceID} {
		token, unknownResult := v.tokenForIdentity(ctx, clusterIdentityURL, identity)
		if unknownResult != nil {
			return *unknownResult
		}

		allowed, unknownResult := v.hasReaderOnDiskEncryptionSet(ctx, checkAccessClient, token, desID)
		if unknownResult != nil {
			return *unknownResult
		}
		if !allowed {
			missing = append(missing, fmt.Sprintf(
				"Managed identity '%s' does not have the Reader role on Disk Encryption Set '%s'. "+
					"Grant it to let an in-progress node pool creation proceed automatically; if creation has "+
					"already failed, grant it and retry the request: "+
					"az role assignment create --assignee-object-id $(az identity show --ids '%s' --query principalId -o tsv) "+
					"--assignee-principal-type ServicePrincipal --role Reader --scope '%s'",
				identity, desID, identity, desID))
		}
	}

	if len(missing) > 0 {
		msg := strings.Join(missing, "\n")
		return FailedValidation("MissingReaderRoleOnDiskEncryptionSet", msg, msg)
	}

	return PassedValidation(
		coreapi.ControllerConditionReasonAsExpected,
		fmt.Sprintf("Required identities have the Reader role on Disk Encryption Set %s.", desID),
		fmt.Sprintf("required identities have the Reader role on disk encryption set %s", desID),
	)
}

// tokenForIdentity mints an MI Dataplane access token for the given cluster identity (the service
// managed identity or a control plane operator). Both are attached to the cluster's top-level ARM
// identity, so MI Dataplane can mint for them; using the token as the CheckAccessV2 subject preserves
// the identity's Entra group memberships (unlike an ObjectId-only subject) and avoids an ARM self-read.
func (v *AzureNodePoolDiskEncryptionSetPermissionsValidation) tokenForIdentity(ctx context.Context, clusterIdentityURL string, identity *azcorearm.ResourceID) (azcore.AccessToken, *ValidationResult) {
	retriever, err := v.miDataplaneBasedAccessTokenRetrieverBuilder.Build(clusterIdentityURL, identity)
	if err != nil {
		result := UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("failed to build access token retriever for identity %s: %s", identity, err),
			ControllerReportingPolicyTypeError,
		)
		return azcore.AccessToken{}, &result
	}
	token, err := retriever.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{v.checkAccessV2Scope}})
	if err != nil {
		result := UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("failed to get access token for identity %s: %s", identity, err),
			ControllerReportingPolicyTypeError,
		)
		return azcore.AccessToken{}, &result
	}
	return token, nil
}

func (v *AzureNodePoolDiskEncryptionSetPermissionsValidation) hasReaderOnDiskEncryptionSet(ctx context.Context, checkAccessClient azureclient.CheckAccessV2Client, token azcore.AccessToken, desID *azcorearm.ResourceID) (bool, *ValidationResult) {
	authRequest, err := checkAccessClient.CreateAuthorizationRequest(desID.String(), []string{diskEncryptionSetReadAction}, token.Token)
	if err != nil {
		result := UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("failed to create authorization request for disk encryption set %s: %s", desID, err),
			ControllerReportingPolicyTypeError,
		)
		return false, &result
	}

	resp, err := checkAccessClient.CheckAccess(ctx, *authRequest)
	if err != nil {
		result := UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("failed to check Reader permission on disk encryption set %s: %s", desID, err),
			ControllerReportingPolicyTypeError,
		)
		return false, &result
	}
	if resp == nil {
		result := UnknownValidation(
			"InternalError",
			"Unable to verify OS disk encryption set permissions.",
			fmt.Sprintf("CheckAccess response for disk encryption set %s is unexpectedly nil, indicating a possible internal failure", desID),
			ControllerReportingPolicyTypeError,
		)
		return false, &result
	}

	for _, decision := range resp.Value {
		if decision.ActionId == diskEncryptionSetReadAction && decision.AccessDecision == checkaccessv2.Allowed {
			return true, nil
		}
	}
	return false, nil
}
