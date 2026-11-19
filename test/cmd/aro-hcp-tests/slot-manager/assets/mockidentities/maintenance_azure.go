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
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/pinning"
)

func preparationDrift(name string, err error) error {
	return fmt.Errorf("identity %s is not safe for initial preparation: %w; external subscription maintenance cannot rotate/repin credentials or replace owners; drain affected slots and use the documented explicit identity rotation/owner workflow", name, err)
}

// PreflightIdentity permits initial creation and safe retry of an interrupted
// first pin. A certificate can be reused only with the expected app/SP binding,
// no existing application credentials, and the exact configured owners.
// Initialized identities are checked without changing credentials or owners.
func (store *azureStore) PreflightIdentity(ctx context.Context, name string, desired []string) (bool, error) {
	application, err := graph.LookupApplication(ctx, store.graph, name, name)
	missingApplication := graph.IsNotFound(err)
	if err != nil && !missingApplication {
		return false, preparationDrift(name, err)
	}
	certificate, certificateErr := store.certificateAPI.GetCertificate(ctx, name, "", nil)
	var response *azcore.ResponseError
	missingCertificate := errors.As(certificateErr, &response) && response.StatusCode == 404
	if certificateErr != nil && !missingCertificate {
		return false, fmt.Errorf("read certificate %s: %w", name, certificateErr)
	}
	if missingApplication {
		if !missingCertificate {
			return false, preparationDrift(name, fmt.Errorf("certificate exists without its application"))
		}
		if err := graph.CheckApplicationDisplayName(ctx, store.graph, name, ""); err != nil {
			return false, preparationDrift(name, err)
		}
		return true, nil
	}
	if len(application.GetKeyCredentials()) > 0 {
		principal, err := store.Resolve(ctx, name, false)
		if err != nil {
			return false, preparationDrift(name, err)
		}
		if err := store.Certificate(ctx, name, false); err != nil {
			return false, preparationDrift(name, err)
		}
		if err := store.Owners(ctx, name, principal, desired, false); err != nil {
			return false, preparationDrift(name, err)
		}
		return false, nil
	}
	applicationID := ptr.Deref(application.GetId(), "")
	clientID := ptr.Deref(application.GetAppId(), "")
	if err := graph.CheckApplicationDisplayName(ctx, store.graph, name, applicationID); err != nil {
		return false, preparationDrift(name, err)
	}
	plans := []ownerPlan{{name: "application " + name, relations: store.applicationOwners(applicationID)}}
	principal, err := graph.LookupServicePrincipal(ctx, store.graph, clientID)
	if err != nil && !graph.IsNotFound(err) {
		return false, preparationDrift(name, err)
	}
	if err == nil {
		principalID := ptr.Deref(principal.GetId(), "")
		plans = append(plans, ownerPlan{name: "service principal " + name, relations: store.servicePrincipalOwners(principalID)})
	}
	if !missingCertificate {
		if len(plans) != 2 {
			return false, preparationDrift(name, fmt.Errorf("certificate exists without its service principal"))
		}
		if certificate.Attributes == nil || !ptr.Deref(certificate.Attributes.Enabled, false) {
			return false, preparationDrift(name, fmt.Errorf("initial certificate is disabled or missing attributes"))
		}
		if err := validatePublicCertificate(certificate.CER, time.Now()); err != nil {
			return false, preparationDrift(name, err)
		}
		if err := reconcileOwnerSets(ctx, plans, desired, false); err != nil {
			return false, preparationDrift(name, err)
		}
		return true, nil
	}
	if err := initializeOwnerSets(ctx, plans, desired, false); err != nil {
		return false, preparationDrift(name, err)
	}
	return true, nil
}

// initializeOwnerSets only adds owners to uninitialized identities. Unlike the
// authoritative pool operation it has no path that removes an owner.
func initializeOwnerSets(ctx context.Context, plans []ownerPlan, desired []string, apply bool) error {
	for index := range plans {
		plan := &plans[index]
		current, err := plan.relations.list(ctx)
		if err != nil {
			return fmt.Errorf("read initial owners of %s: %w", plan.name, err)
		}
		for _, id := range current {
			if !slices.Contains(desired, id) {
				return fmt.Errorf("%s has an owner outside entraAppOwnerIds; initial preparation never removes owners", plan.name)
			}
		}
		plan.missing = nil
		for _, id := range desired {
			if !slices.Contains(current, id) {
				plan.missing = append(plan.missing, id)
			}
		}
	}
	if !apply {
		return nil
	}
	for _, plan := range plans {
		for _, id := range plan.missing {
			if err := plan.relations.add(ctx, id); err != nil {
				return fmt.Errorf("add initial owner to %s: %w", plan.name, err)
			}
		}
	}
	return reconcileOwnerSets(ctx, plans, desired, false)
}

func (store *azureStore) PrepareIdentity(ctx context.Context, name string, desired []string) (leaseenv.MockPrincipal, error) {
	initialize, err := store.PreflightIdentity(ctx, name, desired)
	if err != nil {
		return leaseenv.MockPrincipal{}, err
	}
	if !initialize {
		return store.Resolve(ctx, name, false)
	}
	principal, err := store.Resolve(ctx, name, true)
	if err != nil {
		return principal, fmt.Errorf("create initial application/service principal: %w", err)
	}
	// Recheck after creating missing objects, before changing owners or pins.
	initialize, err = store.PreflightIdentity(ctx, name, desired)
	if err != nil || !initialize {
		return principal, err
	}
	application, err := graph.LookupApplication(ctx, store.graph, name, name)
	if err != nil {
		return principal, fmt.Errorf("read initial application before adding owners: %w", err)
	}
	if application == nil || ptr.Deref(application.GetAppId(), "") != principal.ClientID || ptr.Deref(application.GetId(), "") == "" {
		return principal, preparationDrift(name, fmt.Errorf("application identity changed during initial preparation"))
	}
	plans := []ownerPlan{{name: "application " + name, relations: store.applicationOwners(*application.GetId())}, {name: "service principal " + name, relations: store.servicePrincipalOwners(principal.PrincipalID)}}
	if err := initializeOwnerSets(ctx, plans, desired, true); err != nil {
		return principal, err
	}
	initialize, err = store.PreflightIdentity(ctx, name, desired)
	if err != nil || !initialize {
		return principal, err
	}
	applicationClient := initialApplicationClient{ApplicationClient: store.applications, applicationID: *application.GetId(), clientID: principal.ClientID, name: name}
	if err := pinning.NewPinner(store.certificates, applicationClient).Pin(ctx, []pinning.Binding{{ApplicationDisplayName: name, CertificateName: name, CertificateDNSName: name + ".hcp.osadev.cloud"}}, pinning.Options{CreateMissing: true, Timeout: 2 * time.Minute}); err != nil {
		return principal, fmt.Errorf("pin initial certificate: %w", err)
	}
	if err := store.Certificate(ctx, name, false); err != nil {
		return principal, fmt.Errorf("validate initial pinned certificate: %w", err)
	}
	return principal, nil
}

// initialApplicationClient prevents the general-purpose pinner from replacing
// credentials, including credentials that appeared after the initial preflight.
type initialApplicationClient struct {
	pinning.ApplicationClient
	applicationID string
	clientID      string
	name          string
}

func (client initialApplicationClient) FindApplication(ctx context.Context, name string) (*pinning.Application, error) {
	application, err := client.ApplicationClient.FindApplication(ctx, name)
	if err != nil {
		return nil, err
	}
	if application == nil || application.ID != client.applicationID || application.AppID != client.clientID || application.DisplayName != client.name {
		return nil, preparationDrift(client.name, fmt.Errorf("application binding changed before initial pin"))
	}
	if len(application.KeyCredentials) != 0 {
		return nil, preparationDrift(client.name, fmt.Errorf("application credentials appeared before initial pin"))
	}
	return application, nil
}

func (client initialApplicationClient) ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error {
	application, err := client.ApplicationClient.GetApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	if application == nil || application.ID != client.applicationID || application.AppID != client.clientID || application.DisplayName != client.name || applicationID != client.applicationID || certificateName != client.name {
		return preparationDrift(client.name, fmt.Errorf("application binding changed before initial pin"))
	}
	if len(application.KeyCredentials) != 0 {
		return preparationDrift(client.name, fmt.Errorf("application credentials appeared before initial pin"))
	}
	return client.ApplicationClient.ReplaceKeyCredentials(ctx, applicationID, certificateName, certificate)
}
