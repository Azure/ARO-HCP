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

// Package secrets reconciles the credentials of Microsoft Entra applications
// that authenticate with a client secret: admin consent for their declared
// Graph permissions, and the client secret, client ID and tenant ID in Key Vault.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
)

const (
	// ClientSecretSuffix is the required suffix of a binding's client secret name.
	ClientSecretSuffix = "-client-secret"
	clientIDSuffix     = "-client-id"
	tenantIDSuffix     = "-tenant-id"

	// ResourceAccessRole and ResourceAccessScope are the Graph resourceAccess
	// types for application and delegated permissions.
	ResourceAccessRole  = "Role"
	ResourceAccessScope = "Scope"

	secretLifetime = 2 * 365 * 24 * time.Hour
)

// Binding maps an Entra application display name to the Key Vault secret that
// holds its client secret. The client ID and tenant ID are stored next to it
// under the same prefix: <prefix>-client-secret, <prefix>-client-id and
// <prefix>-tenant-id.
type Binding struct {
	ApplicationDisplayName string
	ClientSecretName       string
}

// Prefix returns the secret name without the "-client-secret" suffix.
func (b Binding) Prefix() string {
	return strings.TrimSuffix(b.ClientSecretName, ClientSecretSuffix)
}

// Options controls reconciliation.
type Options struct {
	// GrantConsent grants admin consent for the application's declared
	// requiredResourceAccess.
	GrantConsent bool
	DryRun       bool
	Timeout      time.Duration
}

// Access is one entry of an application's requiredResourceAccess.
type Access struct {
	ID   string
	Type string
}

// ResourceAccess lists the permissions an application requests on one resource.
type ResourceAccess struct {
	ResourceAppID string
	Access        []Access
}

// Application contains the Graph fields used during reconciliation.
type Application struct {
	ID                     string
	AppID                  string
	RequiredResourceAccess []ResourceAccess
}

// ServicePrincipal contains the Graph fields used during consent.
type ServicePrincipal struct {
	ID string
	// ScopeValues maps delegated permission IDs to their scope names.
	ScopeValues map[string]string
}

// PermissionGrant is a tenant-wide delegated permission grant.
type PermissionGrant struct {
	ID    string
	Scope string
}

// AppRoleAssignment is an application permission granted to a principal.
type AppRoleAssignment struct {
	ResourceID string
	AppRoleID  string
}

// GraphClient is the Microsoft Graph surface used by the Ensurer. Lookups that
// find nothing must return an error wrapping graph.ErrNotVisible.
type GraphClient interface {
	FindApplication(ctx context.Context, displayName string) (*Application, error)
	FindServicePrincipal(ctx context.Context, appID string) (*ServicePrincipal, error)
	TenantID(ctx context.Context) (string, error)
	AddPassword(ctx context.Context, applicationID, displayName string, end time.Time) (string, error)
	ListAppRoleAssignments(ctx context.Context, principalID string) ([]AppRoleAssignment, error)
	CreateAppRoleAssignment(ctx context.Context, principalID, resourceID, appRoleID string) error
	// FindPermissionGrant returns nil when the client has no tenant-wide grant on the resource.
	FindPermissionGrant(ctx context.Context, clientID, resourceID string) (*PermissionGrant, error)
	CreatePermissionGrant(ctx context.Context, clientID, resourceID, scope string) error
	UpdatePermissionGrant(ctx context.Context, grantID, scope string) error
}

// SecretStore reads and writes Key Vault secrets. Get returns ("", false, nil)
// when the secret does not exist.
type SecretStore interface {
	Get(ctx context.Context, name string) (string, bool, error)
	Set(ctx context.Context, name, value string) error
}

// Ensurer reconciles application credentials.
type Ensurer struct {
	graph GraphClient
	store SecretStore
	now   func() time.Time
}

// EnsurerOption configures an Ensurer.
type EnsurerOption func(*Ensurer)

// WithClock sets the clock used to compute client secret expiry.
func WithClock(now func() time.Time) EnsurerOption {
	return func(e *Ensurer) {
		e.now = now
	}
}

// NewEnsurer creates a credential reconciler.
func NewEnsurer(graph GraphClient, store SecretStore, options ...EnsurerOption) *Ensurer {
	ensurer := &Ensurer{graph: graph, store: store, now: time.Now}
	for _, option := range options {
		option(ensurer)
	}
	return ensurer
}

// Ensure reconciles every binding and returns all failures joined. Existing
// secrets are never regenerated or overwritten.
func (e *Ensurer) Ensure(ctx context.Context, bindings []Binding, options Options) error {
	if options.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}

	var errs []error
	for _, binding := range bindings {
		bindingCtx, cancel := context.WithTimeout(ctx, options.Timeout)
		err := e.ensureOne(bindingCtx, binding, options)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("application %q: %w", binding.ApplicationDisplayName, err))
		}
	}
	return errors.Join(errs...)
}

func (e *Ensurer) ensureOne(ctx context.Context, binding Binding, options Options) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("application", binding.ApplicationDisplayName)

	application, err := e.graph.FindApplication(ctx, binding.ApplicationDisplayName)
	if err != nil {
		return fmt.Errorf("find application: %w", err)
	}

	if options.GrantConsent {
		err := e.grantConsent(ctx, logger, application, options.DryRun)
		if err != nil {
			return fmt.Errorf("grant admin consent: %w", err)
		}
	}

	prefix := binding.Prefix()
	err = e.ensureClientSecret(ctx, logger, application, prefix+ClientSecretSuffix, options.DryRun)
	if err != nil {
		return err
	}
	err = e.ensurePlain(ctx, logger, prefix+clientIDSuffix, options.DryRun, func(context.Context) (string, error) {
		return application.AppID, nil
	})
	if err != nil {
		return err
	}
	return e.ensurePlain(ctx, logger, prefix+tenantIDSuffix, options.DryRun, e.graph.TenantID)
}

// ensureClientSecret mints and stores a client secret unless one is stored already.
func (e *Ensurer) ensureClientSecret(ctx context.Context, logger logr.Logger, application *Application, name string, dryRun bool) error {
	_, found, err := e.store.Get(ctx, name)
	switch {
	case err != nil:
		return fmt.Errorf("check secret %q: %w", name, err)
	case found:
		logger.Info("client secret already present", "secret", name)
		return nil
	case dryRun:
		logger.Info("would mint client secret", "secret", name)
		return nil
	}

	logger.Info("minting client secret", "secret", name)
	value, err := e.graph.AddPassword(ctx, application.ID, name+"-managed", e.now().Add(secretLifetime))
	if err != nil {
		return fmt.Errorf("mint client secret: %w", err)
	}
	err = e.store.Set(ctx, name, value)
	if err != nil {
		return fmt.Errorf("store secret %q: %w", name, err)
	}
	return nil
}

// ensurePlain stores a non-sensitive derived value unless the secret exists.
func (e *Ensurer) ensurePlain(ctx context.Context, logger logr.Logger, name string, dryRun bool, value func(context.Context) (string, error)) error {
	_, found, err := e.store.Get(ctx, name)
	switch {
	case err != nil:
		return fmt.Errorf("check secret %q: %w", name, err)
	case found:
		logger.Info("secret already present", "secret", name)
		return nil
	case dryRun:
		logger.Info("would store secret", "secret", name)
		return nil
	}

	resolved, err := value(ctx)
	if err != nil {
		return fmt.Errorf("resolve value for secret %q: %w", name, err)
	}
	if resolved == "" {
		return fmt.Errorf("resolved an empty value for secret %q", name)
	}

	logger.Info("storing secret", "secret", name)
	err = e.store.Set(ctx, name, resolved)
	if err != nil {
		return fmt.Errorf("store secret %q: %w", name, err)
	}
	return nil
}
