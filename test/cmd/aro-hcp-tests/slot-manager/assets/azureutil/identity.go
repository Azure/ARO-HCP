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

package azureutil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

// ParsePrincipalID requires a hyphenated UUID without surrounding whitespace.
func ParsePrincipalID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, err
	}
	if !strings.EqualFold(value, id.String()) {
		return uuid.Nil, errors.New("principal ID must be a hyphenated UUID without surrounding whitespace")
	}
	return id, nil
}

// NormalizedNameSet lowercases names for case-insensitive identity matching.
func NormalizedNameSet(names []string) sets.Set[string] {
	normalized := sets.New[string]()
	for _, name := range names {
		normalized.Insert(strings.ToLower(name))
	}
	return normalized
}

// RequireIdentities fails closed when any expected identity is missing from actual.
// actual maps lowercase names to the Azure-reported spelling.
func RequireIdentities(resourceGroup string, expected sets.Set[string], actual map[string]string) error {
	missing := sets.List(expected.Difference(sets.KeySet(actual)))
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("identity inventory drift in resource group %q: missing=%v", resourceGroup, missing)
}

// IdentityNameDiff reports missing expected names and unexpected Azure-reported
// names for one resource group. reported maps lowercase names to Azure spelling.
func IdentityNameDiff(expected sets.Set[string], reported map[string]string) (missing, unexpected []string) {
	actual := sets.KeySet(reported)
	missing = sets.List(expected.Difference(actual))
	for _, normalized := range sets.List(actual.Difference(expected)) {
		unexpected = append(unexpected, reported[normalized])
	}
	return missing, unexpected
}

// UniqueReportedNames maps lowercase identity names to Azure-reported spelling.
func UniqueReportedNames(resourceGroup string, actual []string) (map[string]string, error) {
	reported := map[string]string{}
	for _, name := range actual {
		normalized := strings.ToLower(name)
		if _, found := reported[normalized]; found {
			return nil, fmt.Errorf("identity list for resource group %q returned duplicate identity %q", resourceGroup, name)
		}
		reported[normalized] = name
	}
	return reported, nil
}

// IsNotFound reports whether err is an Azure SDK 404.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
}

// ClusterProfileCredential builds a token credential from the selected cluster profile.
func ClusterProfileCredential(profileDir string) (azcore.TokenCredential, error) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return nil, errors.New("selected cluster profile dir is empty")
	}
	credential, err := slots.NewClusterProfileCredential(profileDir, nil)
	if err != nil {
		return nil, fmt.Errorf("failed constructing cluster profile credential: %w", err)
	}
	return credential, nil
}

// InspectedIdentity is one expected user-assigned identity in a leased group.
type InspectedIdentity struct {
	Name        string
	PrincipalID uuid.UUID
}

// InspectResourceGroup lists identities in resourceGroup, requires expectedNames,
// and returns only those identities. Unexpected names are logged and excluded.
func InspectResourceGroup(ctx context.Context, client *armmsi.UserAssignedIdentitiesClient, resourceGroup string, expectedNames []string) (map[string]InspectedIdentity, error) {
	expected := NormalizedNameSet(expectedNames)
	reported := map[string]string{}
	identities := map[string]InspectedIdentity{}
	var unexpected []string
	pager := client.NewListByResourceGroupPager(resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing identities in resource group %q: %w", resourceGroup, err)
		}
		for _, identity := range page.Value {
			name, err := ListedIdentityName(identity, resourceGroup)
			if err != nil {
				return nil, err
			}
			normalized := strings.ToLower(name)
			if !expected.Has(normalized) {
				unexpected = append(unexpected, name)
				continue
			}
			principalID, err := listedIdentityPrincipal(identity, name, resourceGroup)
			if err != nil {
				return nil, err
			}
			if _, found := identities[normalized]; found {
				return nil, fmt.Errorf("identity list for resource group %q returned duplicate identity %q", resourceGroup, name)
			}
			reported[normalized] = name
			identities[normalized] = InspectedIdentity{Name: name, PrincipalID: principalID}
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		logr.FromContextOrDiscard(ctx).Info("Unexpected identities in leased resource group; excluded from admission cleanup",
			"resourceGroup", resourceGroup,
			"identityNames", unexpected,
		)
	}
	if err := RequireIdentities(resourceGroup, expected, reported); err != nil {
		return nil, err
	}
	return identities, nil
}

// ListedIdentityName returns the Azure-reported identity name.
func ListedIdentityName(identity *armmsi.Identity, resourceGroup string) (string, error) {
	if identity == nil || identity.Name == nil || strings.TrimSpace(*identity.Name) == "" {
		return "", fmt.Errorf("identity list for resource group %q returned an entry without a name", resourceGroup)
	}
	return *identity.Name, nil
}

func listedIdentityPrincipal(identity *armmsi.Identity, name, resourceGroup string) (uuid.UUID, error) {
	if identity.Properties == nil || identity.Properties.PrincipalID == nil || strings.TrimSpace(*identity.Properties.PrincipalID) == "" {
		return uuid.Nil, fmt.Errorf("identity %q in resource group %q has no principal ID", name, resourceGroup)
	}
	principalID, err := ParsePrincipalID(*identity.Properties.PrincipalID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity %q has invalid principal ID: %w", name, err)
	}
	return principalID, nil
}

// FederatedCredentialRef identifies one federated credential on a user-assigned identity.
type FederatedCredentialRef struct {
	ResourceGroup string
	IdentityName  string
	Name          string
}

// ListFederatedCredentials lists every federated credential on identityName.
func ListFederatedCredentials(ctx context.Context, client *armmsi.FederatedIdentityCredentialsClient, resourceGroup, identityName string) ([]FederatedCredentialRef, error) {
	var refs []FederatedCredentialRef
	pager := client.NewListPager(resourceGroup, identityName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing federated identity credentials for identity %q in resource group %q: %w", identityName, resourceGroup, err)
		}
		for _, credential := range page.Value {
			name, err := listedFederatedCredentialName(credential, identityName, resourceGroup)
			if err != nil {
				return nil, err
			}
			refs = append(refs, FederatedCredentialRef{
				ResourceGroup: resourceGroup,
				IdentityName:  identityName,
				Name:          name,
			})
		}
	}
	return refs, nil
}

func listedFederatedCredentialName(credential *armmsi.FederatedIdentityCredential, identityName, resourceGroup string) (string, error) {
	if credential == nil || credential.Name == nil || strings.TrimSpace(*credential.Name) == "" {
		return "", fmt.Errorf("federated identity credential list for identity %q in resource group %q returned an entry without a name", identityName, resourceGroup)
	}
	return *credential.Name, nil
}

// RoleAssignmentsForPrincipals lists subscription role assignments whose principal is in principals.
func RoleAssignmentsForPrincipals(ctx context.Context, client *armauthorization.RoleAssignmentsClient, principals sets.Set[uuid.UUID]) ([]*armauthorization.RoleAssignment, error) {
	var assignments []*armauthorization.RoleAssignment
	pager := client.NewListForSubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing role assignments for subscription: %w", err)
		}
		for _, assignment := range page.Value {
			principalID, err := roleAssignmentPrincipal(assignment)
			if err != nil {
				return nil, err
			}
			if principals.Has(principalID) {
				assignments = append(assignments, assignment)
			}
		}
	}
	return assignments, nil
}

func roleAssignmentPrincipal(assignment *armauthorization.RoleAssignment) (uuid.UUID, error) {
	if assignment == nil || assignment.Properties == nil || assignment.Properties.PrincipalID == nil || strings.TrimSpace(*assignment.Properties.PrincipalID) == "" {
		return uuid.Nil, errors.New("role assignment list returned an entry without a principal ID")
	}
	principalID, err := ParsePrincipalID(*assignment.Properties.PrincipalID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("role assignment list returned invalid principal ID %q: %w", *assignment.Properties.PrincipalID, err)
	}
	return principalID, nil
}

// DeleteRoleAssignmentOps returns bounded-worker operations that delete assignments by ID.
func DeleteRoleAssignmentOps(logger logr.Logger, client *armauthorization.RoleAssignmentsClient, assignments []*armauthorization.RoleAssignment) []func(context.Context) error {
	operations := make([]func(context.Context) error, 0, len(assignments))
	for _, assignment := range assignments {
		operations = append(operations, func(ctx context.Context) error {
			if assignment.ID == nil {
				return errors.New("role assignment has no ID")
			}
			logger.Info("Attempting to delete role assignment", "roleAssignmentResourceID", *assignment.ID)
			_, err := client.DeleteByID(ctx, *assignment.ID, nil)
			if IsNotFound(err) {
				logger.Info("Role assignment not found. Nothing to delete", "roleAssignmentResourceID", *assignment.ID)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting role assignment %q: %w", *assignment.ID, err)
			}
			logger.Info("Deleted role assignment",
				"roleAssignmentResourceID", *assignment.ID,
				"roleAssignmentPrincipalID", *assignment.Properties.PrincipalID,
				"roleAssignmentRoleDefinitionResourceID", assignment.Properties.RoleDefinitionID,
				"roleAssignmentScope", ptr.Deref(assignment.Properties.Scope, "<nil>"),
				"roleAssignmentCreatedBy", ptr.Deref(assignment.Properties.CreatedBy, "<nil>"),
				"roleAssignmentCreatedOn", ptr.Deref(assignment.Properties.CreatedOn, time.Time{}),
			)
			return nil
		})
	}
	return operations
}

// DeleteFederatedCredentialOps returns bounded-worker operations that delete federated credentials.
func DeleteFederatedCredentialOps(logger logr.Logger, client *armmsi.FederatedIdentityCredentialsClient, credentials []FederatedCredentialRef) []func(context.Context) error {
	operations := make([]func(context.Context) error, 0, len(credentials))
	for _, credential := range credentials {
		operations = append(operations, func(ctx context.Context) error {
			logger.Info("Attempting to delete federated identity credential",
				"resourceGroup", credential.ResourceGroup,
				"identityName", credential.IdentityName,
				"federatedCredentialName", credential.Name,
			)
			_, err := client.Delete(ctx, credential.ResourceGroup, credential.IdentityName, credential.Name, nil)
			if IsNotFound(err) {
				logger.Info("Federated identity credential not found. Nothing to delete",
					"resourceGroup", credential.ResourceGroup,
					"identityName", credential.IdentityName,
					"federatedCredentialName", credential.Name,
				)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed deleting federated identity credential %q on identity %q in resource group %q: %w",
					credential.Name, credential.IdentityName, credential.ResourceGroup, err)
			}
			logger.Info("Deleted federated identity credential",
				"resourceGroup", credential.ResourceGroup,
				"identityName", credential.IdentityName,
				"federatedCredentialName", credential.Name,
			)
			return nil
		})
	}
	return operations
}
