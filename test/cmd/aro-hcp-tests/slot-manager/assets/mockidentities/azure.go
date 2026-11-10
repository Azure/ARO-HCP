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

package mockidentities

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/applicationswithuniquename"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/pinning"
)

type azureStore struct {
	armOptions     *azcorearm.ClientOptions
	credential     azcore.TokenCredential
	graph          *msgraph.GraphServiceClient
	certificates   pinning.CertificateClient
	certificateAPI *azcertificates.Client
	applications   pinning.ApplicationClient
}

func newAzureStore(credential azcore.TokenCredential) (identityStore, error) {
	client, err := msgraph.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return nil, err
	}
	certificates, err := azcertificates.NewClient(VaultURL, credential, nil)
	if err != nil {
		return nil, err
	}
	applications, err := pinning.NewGraphClient(credential)
	if err != nil {
		return nil, err
	}
	return &azureStore{credential: credential, graph: client, certificates: pinning.NewCertificateClient(certificates), certificateAPI: certificates, applications: applications}, nil
}

func (store *azureStore) Resolve(ctx context.Context, name string, apply bool) (leaseenv.MockPrincipal, error) {
	result := leaseenv.MockPrincipal{CertificateName: name}
	application, err := store.graph.ApplicationsWithUniqueName(&name).Get(ctx, nil)
	if graph.IsNotFound(err) && apply {
		body := models.NewApplication()
		body.SetDisplayName(&name)
		body.SetSignInAudience(ptr.To("AzureADMyOrg"))
		application, err = store.graph.ApplicationsWithUniqueName(&name).Patch(ctx, body, nil)
	}
	if err != nil {
		return result, err
	}
	if application == nil || ptr.Deref(application.GetId(), "") == "" || ptr.Deref(application.GetAppId(), "") == "" || ptr.Deref(application.GetDisplayName(), "") != name {
		return result, fmt.Errorf("application uniqueName %q has unexpected identity", name)
	}
	first, err := store.graph.Applications().Get(ctx, &applications.ApplicationsRequestBuilderGetRequestConfiguration{QueryParameters: &applications.ApplicationsRequestBuilderGetQueryParameters{Filter: ptr.To("displayName eq '" + graph.QuoteFilterValue(name) + "'"), Select: []string{"id"}}})
	if err != nil {
		return result, err
	}
	matches, err := graph.CollectPages(ctx, first, func(ctx context.Context, link string) (models.ApplicationCollectionResponseable, error) {
		return store.graph.Applications().WithUrl(link).Get(ctx, nil)
	})
	if err != nil {
		return result, err
	}
	if len(matches) != 1 || matches[0] == nil || ptr.Deref(matches[0].GetId(), "") != *application.GetId() {
		return result, fmt.Errorf("application displayName %q missing or ambiguous (%d matches)", name, len(matches))
	}
	principal, err := store.graph.ServicePrincipalsWithAppId(application.GetAppId()).Get(ctx, nil)
	if graph.IsNotFound(err) && apply {
		body := models.NewServicePrincipal()
		body.SetDisplayName(&name)
		principal, err = store.graph.ServicePrincipalsWithAppId(application.GetAppId()).Patch(ctx, body, nil)
	}
	if err != nil {
		return result, err
	}
	if principal == nil || ptr.Deref(principal.GetAppId(), "") != *application.GetAppId() || ptr.Deref(principal.GetId(), "") == "" || !ptr.Deref(principal.GetAccountEnabled(), false) {
		return result, fmt.Errorf("service principal for %q is missing, disabled or mismatched", name)
	}
	result.ClientID = *application.GetAppId()
	result.PrincipalID = *principal.GetId()
	return result, nil
}

func (store *azureStore) Certificate(ctx context.Context, name string, apply bool) error {
	if apply {
		if err := pinning.NewPinner(store.certificates, store.applications).Pin(ctx, []pinning.Binding{{ApplicationDisplayName: name, CertificateName: name, CertificateDNSName: name + ".hcp.osadev.cloud"}}, pinning.Options{CreateMissing: true, Timeout: 2 * time.Minute}); err != nil {
			return err
		}
	}
	certificate, err := store.certificateAPI.GetCertificate(ctx, name, "", nil)
	if err != nil {
		return err
	}
	application, err := store.graph.ApplicationsWithUniqueName(&name).Get(ctx, &applicationswithuniquename.ApplicationsWithUniqueNameRequestBuilderGetRequestConfiguration{QueryParameters: &applicationswithuniquename.ApplicationsWithUniqueNameRequestBuilderGetQueryParameters{Select: []string{"keyCredentials"}}})
	if err != nil {
		return err
	}
	if certificate.Attributes == nil || !ptr.Deref(certificate.Attributes.Enabled, false) {
		return fmt.Errorf("certificate is disabled or missing attributes")
	}
	if application == nil {
		return fmt.Errorf("application has no credential metadata")
	}
	return validateCertificate(certificate.CER, application.GetKeyCredentials(), time.Now())
}

func validateCertificate(certificate []byte, keys []models.KeyCredentialable, now time.Time) error {
	public, err := x509.ParseCertificate(certificate)
	if err != nil {
		return fmt.Errorf("invalid public certificate: %w", err)
	}
	if now.Before(public.NotBefore) || !now.Before(public.NotAfter) {
		return fmt.Errorf("certificate is not currently valid; Owner must explicitly rotate it")
	}
	thumbprint := sha1.Sum(certificate)
	if len(keys) != 1 {
		return fmt.Errorf("expected exactly one pinned key credential")
	}
	key := keys[0]
	if key == nil {
		return fmt.Errorf("missing application credential metadata")
	}
	if ptr.Deref(key.GetTypeEscaped(), "") != "AsymmetricX509Cert" || ptr.Deref(key.GetUsage(), "") != "Verify" || !bytes.Equal(key.GetCustomKeyIdentifier(), thumbprint[:]) {
		return fmt.Errorf("certificate thumbprint is not pinned on application")
	}
	if key.GetStartDateTime() == nil || key.GetEndDateTime() == nil || now.Before(*key.GetStartDateTime()) || !now.Before(*key.GetEndDateTime()) {
		return fmt.Errorf("pinned application credential is not currently valid")
	}
	return nil
}

const (
	contributorRole       = "b24988ac-6180-42a0-ab88-20f7382dd24c"
	rbacAdministratorRole = "f58310d9-a9f6-439a-9e8d-f62e7b41a168"
	cryptoUserRole        = "12338af0-0e69-4776-bea7-57ae8d297424"
)

type roleBinding struct{ principal, role string }

func (store *azureStore) AuthorizeOwner(ctx context.Context, subscription string) error {
	token, err := store.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
	if err != nil {
		return fmt.Errorf("obtain Owner credential: %w", err)
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token.Token, claims); err != nil {
		return fmt.Errorf("cannot identify Owner credential")
	}
	objectID, ok := claims["oid"].(string)
	if !ok || objectID == "" {
		return fmt.Errorf("Owner credential has no object ID")
	}
	client, err := armauthorization.NewRoleAssignmentsClient(subscription, store.credential, store.armOptions)
	if err != nil {
		return err
	}
	pager := client.NewListForScopePager("/subscriptions/"+subscription, &armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: ptr.To("atScope()")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil {
				continue
			}
			properties := assignment.Properties
			if strings.EqualFold(ptr.Deref(properties.PrincipalID, ""), objectID) && strings.HasSuffix(strings.ToLower(ptr.Deref(properties.RoleDefinitionID, "")), "/8e3af657-a8ff-443c-a75c-2fe8c4bcb635") && ptr.Deref(properties.Condition, "") == "" {
				return nil
			}
		}
	}
	return fmt.Errorf("apply requires an unconditional Owner assignment to the invoking principal at the customer subscription or an ancestor; group-only and eligible-but-inactive Owner assignments are not accepted")
}

func bindings(bundle leaseenv.MockIdentityBundlePayload, msiRole string) []roleBinding {
	return []roleBinding{
		{bundle.MSI.PrincipalID, msiRole}, {bundle.MSI.PrincipalID, cryptoUserRole},
		{bundle.Backend.PrincipalID, contributorRole}, {bundle.Backend.PrincipalID, rbacAdministratorRole},
		{bundle.ClustersService.PrincipalID, contributorRole}, {bundle.ClustersService.PrincipalID, rbacAdministratorRole},
	}
}

func (store *azureStore) Roles(ctx context.Context, subscription string, bundle leaseenv.MockIdentityBundlePayload, apply bool) error {
	scope := "/subscriptions/" + subscription
	definitions, err := armauthorization.NewRoleDefinitionsClient(store.credential, store.armOptions)
	if err != nil {
		return err
	}
	definitionPager := definitions.NewListPager(scope, &armauthorization.RoleDefinitionsClientListOptions{Filter: ptr.To("roleName eq 'dev-msi-mock-" + subscription + "'")})
	var roleIDs []string
	for definitionPager.More() {
		page, err := definitionPager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, definition := range page.Value {
			if definition != nil && definition.ID != nil {
				roleIDs = append(roleIDs, *definition.ID)
			}
		}
	}
	if len(roleIDs) != 1 {
		return fmt.Errorf("expected subscription-local dev-msi-mock role; Owner must deploy e2e-subscription-rbac-assignment-subscription.bicep first")
	}
	roleID := roleIDs[0]
	if !strings.HasPrefix(strings.ToLower(roleID), strings.ToLower(scope)+"/providers/microsoft.authorization/roledefinitions/") {
		return fmt.Errorf("MSI role is not subscription-local")
	}
	client, err := armauthorization.NewRoleAssignmentsClient(subscription, store.credential, store.armOptions)
	if err != nil {
		return err
	}
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: ptr.To("atScope()")})
	var assignments []*armauthorization.RoleAssignment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		assignments = append(assignments, page.Value...)
	}
	for _, binding := range bindings(bundle, roleID[strings.LastIndex(roleID, "/")+1:]) {
		definitionID := scope + "/providers/Microsoft.Authorization/roleDefinitions/" + binding.role
		found := false
		for _, assignment := range assignments {
			if assignment == nil || assignment.Properties == nil {
				continue
			}
			properties := assignment.Properties
			if strings.EqualFold(ptr.Deref(properties.Scope, ""), scope) && strings.EqualFold(ptr.Deref(properties.PrincipalID, ""), binding.principal) && strings.EqualFold(ptr.Deref(properties.RoleDefinitionID, ""), definitionID) && ptr.Deref(properties.Condition, "") == "" {
				found = true
			}
		}
		if found {
			continue
		}
		if !apply {
			return fmt.Errorf("missing persistent role %s for %s at customer subscription %s", binding.role, binding.principal, subscription)
		}
		name := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.ToLower(scope+"/"+binding.principal+"/"+binding.role))).String()
		_, err := client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{Properties: &armauthorization.RoleAssignmentProperties{PrincipalID: &binding.principal, RoleDefinitionID: &definitionID, PrincipalType: ptr.To(armauthorization.PrincipalTypeServicePrincipal)}}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}
