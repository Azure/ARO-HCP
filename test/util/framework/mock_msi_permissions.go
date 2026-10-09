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
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

const (
	// LeasedMSIMockSPEnvvar is the Boskos key for the job's MSI mock SP
	// (for example aro-hcp-msi-mock-cs-sp-dev-0). Provisioning already consumes
	// this to override backend/CS Helm values; the DEV missing-identity spec
	// needs the same value (or ARO_HCP_MSI_MOCK_PRINCIPAL_ID) in the test process.
	LeasedMSIMockSPEnvvar = "LEASED_MSI_MOCK_SP"

	// MSIMockPrincipalIDEnvvar is an explicit principal-id override. It must
	// still resolve to a pooled mock SP; the shared personal-dev identity is
	// rejected. If LEASED_MSI_MOCK_SP is also set, both must resolve to the
	// same principal (ARO-29290 provision export).
	MSIMockPrincipalIDEnvvar = "ARO_HCP_MSI_MOCK_PRINCIPAL_ID"

	// MSIMockPoolCatalogEnvvar overrides the path to msi-mock-pool.yaml.
	MSIMockPoolCatalogEnvvar = "ARO_HCP_MSI_MOCK_POOL_CATALOG"

	// keyVaultCryptoUserRoleDefinitionGUID is the built-in Key Vault Crypto
	// User role granted to every MSI mock principal
	// (dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep).
	keyVaultCryptoUserRoleDefinitionGUID = "12338af0-0e69-4776-bea7-57ae8d297424"

	// sharedMockMSIPrincipalID is aro-dev-msi-mock2 (config.yaml miMockPrincipalId).
	// Stripping it would take down personal-dev and any job that did not lease a
	// pool member. The strip helper refuses this principal.
	sharedMockMSIPrincipalID = "d6b62dfa-87f5-49b3-bbcb-4a687c4faa96"

	msiMockRoleNamePrefix     = "dev-msi-mock"
	msiMockPoolCatalogRelPath = "dev-infrastructure/openshift-ci/msi-mock-pool.yaml"
)

var (
	// ErrSharedMockMSIPrincipal is returned when the resolved principal is the
	// shared personal-dev / default CI mock SP.
	ErrSharedMockMSIPrincipal = errors.New("refusing to strip permissions on the shared personal-dev MSI mock principal aro-dev-msi-mock2")

	// ErrMockMSIPrincipalUnresolved is returned when neither a lease name nor an
	// explicit principal id is available to the test process.
	ErrMockMSIPrincipalUnresolved = errors.New("could not resolve a leased MSI mock principal; set " + LeasedMSIMockSPEnvvar + " or " + MSIMockPrincipalIDEnvvar)

	// ErrMockMSIPrincipalNotPooled is returned when the resolved principal is not
	// in msi-mock-pool.yaml.
	ErrMockMSIPrincipalNotPooled = errors.New("resolved MSI mock principal is not a pooled mock SP; refusing to strip")

	// ErrMockMSIPrincipalMismatch is returned when both a Boskos lease and an
	// explicit principal id are set and they do not resolve to the same SP.
	ErrMockMSIPrincipalMismatch = errors.New(LeasedMSIMockSPEnvvar + " and " + MSIMockPrincipalIDEnvvar + " resolve to different principals")
)

// MockMSIRoleAssignmentSnapshot is one subscription-scoped role assignment that
// was deleted so it can be recreated with the same assignment GUID. Keeping the
// GUID matches Bicep's guid(subscription().id, principalId, roleDefinitionId)
// so a later mock-identity-rbac apply is a no-op once restore succeeds.
type MockMSIRoleAssignmentSnapshot struct {
	AssignmentID     string
	Name             string
	Scope            string
	PrincipalID      string
	RoleDefinitionID string
	PrincipalType    armauthorization.PrincipalType
}

type msiMockPoolCatalogFile struct {
	MIMockPool map[string]msiMockPoolEntry `json:"miMockPool"`
}

type msiMockPoolEntry struct {
	ClientID    string `json:"clientId"`
	PrincipalID string `json:"principalId"`
	CertName    string `json:"certName"`
}

// ResolveLeasedMockMSIPrincipalID returns the object id of the MSI mock SP this
// job's backend is impersonating. When LEASED_MSI_MOCK_SP is set, that lease is
// the source of truth. An explicit ARO_HCP_MSI_MOCK_PRINCIPAL_ID must match the
// lease when both are set. It never returns the shared aro-dev-msi-mock2
// principal.
func ResolveLeasedMockMSIPrincipalID() (string, error) {
	catalog, err := loadMSIMockPoolCatalog()
	if err != nil {
		return "", err
	}
	return resolveLeasedMockMSIPrincipalID(os.Getenv(MSIMockPrincipalIDEnvvar), os.Getenv(LeasedMSIMockSPEnvvar), catalog)
}

func resolveLeasedMockMSIPrincipalID(explicitPrincipalID, leasedSP string, catalog msiMockPoolCatalogFile) (string, error) {
	pooled := pooledPrincipalIDs(catalog)
	explicitPrincipalID = strings.TrimSpace(explicitPrincipalID)
	leasedSP = strings.TrimSpace(leasedSP)

	var leasedPrincipal string
	if leasedSP != "" {
		entry, ok := catalog.MIMockPool[leasedSP]
		if !ok || strings.TrimSpace(entry.PrincipalID) == "" {
			return "", fmt.Errorf("%w: lease %q not in MSI mock pool catalog", ErrMockMSIPrincipalUnresolved, leasedSP)
		}
		leasedPrincipal = strings.TrimSpace(entry.PrincipalID)
		if strings.EqualFold(leasedPrincipal, sharedMockMSIPrincipalID) {
			return "", ErrSharedMockMSIPrincipal
		}
	}

	if explicitPrincipalID != "" {
		if strings.EqualFold(explicitPrincipalID, sharedMockMSIPrincipalID) {
			return "", ErrSharedMockMSIPrincipal
		}
		if !pooled[strings.ToLower(explicitPrincipalID)] {
			return "", fmt.Errorf("%w: %s", ErrMockMSIPrincipalNotPooled, explicitPrincipalID)
		}
		if leasedPrincipal != "" && !strings.EqualFold(explicitPrincipalID, leasedPrincipal) {
			return "", fmt.Errorf("%w: %s=%s resolved to %s, %s=%s", ErrMockMSIPrincipalMismatch, LeasedMSIMockSPEnvvar, leasedSP, leasedPrincipal, MSIMockPrincipalIDEnvvar, explicitPrincipalID)
		}
		return explicitPrincipalID, nil
	}

	if leasedPrincipal != "" {
		return leasedPrincipal, nil
	}
	return "", ErrMockMSIPrincipalUnresolved
}

func pooledPrincipalIDs(catalog msiMockPoolCatalogFile) map[string]bool {
	out := make(map[string]bool, len(catalog.MIMockPool))
	for _, entry := range catalog.MIMockPool {
		if entry.PrincipalID == "" {
			continue
		}
		out[strings.ToLower(entry.PrincipalID)] = true
	}
	return out
}

func loadMSIMockPoolCatalog() (msiMockPoolCatalogFile, error) {
	path, err := msiMockPoolCatalogPath()
	if err != nil {
		return msiMockPoolCatalogFile{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return msiMockPoolCatalogFile{}, fmt.Errorf("failed to read MSI mock pool catalog %s: %w", path, err)
	}
	var catalog msiMockPoolCatalogFile
	if err := yaml.Unmarshal(raw, &catalog); err != nil {
		return msiMockPoolCatalogFile{}, fmt.Errorf("failed to parse MSI mock pool catalog %s: %w", path, err)
	}
	return catalog, nil
}

func msiMockPoolCatalogPath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(MSIMockPoolCatalogEnvvar)); explicit != "" {
		return explicit, nil
	}
	if root, err := repoRoot(); err == nil {
		candidate := filepath.Join(root, msiMockPoolCatalogRelPath)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve MSI mock pool catalog: %w", err)
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, msiMockPoolCatalogRelPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("failed to find %s from %s; set %s", msiMockPoolCatalogRelPath, cwd, MSIMockPoolCatalogEnvvar)
		}
		dir = parent
	}
}

// isStrippableMockMSIRole reports whether a subscription-scoped assignment is
// one of the two grants that make the mock SP a stand-in for every operator.
func isStrippableMockMSIRole(roleDefinitionID, roleDefinitionName string) bool {
	if strings.Contains(strings.ToLower(roleDefinitionID), keyVaultCryptoUserRoleDefinitionGUID) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(roleDefinitionName), msiMockRoleNamePrefix)
}

func roleAssignmentNameFromID(assignmentID string) string {
	assignmentID = strings.TrimSuffix(assignmentID, "/")
	i := strings.LastIndex(assignmentID, "/")
	if i < 0 || i+1 >= len(assignmentID) {
		return ""
	}
	return assignmentID[i+1:]
}

const (
	roleAssignmentExistsErrorCode = "RoleAssignmentExists"
	mockMSIRestoreCreateAttempts  = 5
	mockMSIRestoreRetryDelay      = 2 * time.Second
)

type restoreCreateResult int

const (
	restoreCreateOK restoreCreateResult = iota
	restoreCreateRetryTombstone
	restoreCreateFailed
)

func isRoleAssignmentExistsError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.ErrorCode == roleAssignmentExistsErrorCode
}

func isRoleAssignmentNotFoundError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

// interpretRestoreCreate classifies a Role Assignments Create error.
// Only ARM ErrorCode RoleAssignmentExists is success; a generic HTTP 409
// (including ErrorCode Conflict) is a tombstone that must be GET-verified
// and retried, not swallowed.
func interpretRestoreCreate(err error) restoreCreateResult {
	if err == nil || isRoleAssignmentExistsError(err) {
		return restoreCreateOK
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
		return restoreCreateRetryTombstone
	}
	return restoreCreateFailed
}

func restoreOneAssignmentWithWait(ctx context.Context, snapshot MockMSIRoleAssignmentSnapshot, create, get func() error, wait func(time.Duration)) error {
	var lastErr error
	for attempt := 1; attempt <= mockMSIRestoreCreateAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = create()
		switch interpretRestoreCreate(lastErr) {
		case restoreCreateOK:
			return nil
		case restoreCreateFailed:
			return fmt.Errorf("failed to restore mock-MSI role assignment %s: %w", snapshot.AssignmentID, lastErr)
		case restoreCreateRetryTombstone:
			getErr := get()
			if getErr == nil {
				return nil
			}
			if !isRoleAssignmentNotFoundError(getErr) {
				return fmt.Errorf("failed to restore mock-MSI role assignment %s: create conflicted (%w) and GET failed: %v", snapshot.AssignmentID, lastErr, getErr)
			}
			if attempt == mockMSIRestoreCreateAttempts {
				return fmt.Errorf("failed to restore mock-MSI role assignment %s: still missing after %d creates (%w)", snapshot.AssignmentID, mockMSIRestoreCreateAttempts, lastErr)
			}
			wait(mockMSIRestoreRetryDelay)
		}
	}
	return fmt.Errorf("failed to restore mock-MSI role assignment %s: %w", snapshot.AssignmentID, lastErr)
}

func errIfNoStrippableMockMSIAssignments(snapshots []MockMSIRoleAssignmentSnapshot, principalID, subscriptionScope string) error {
	if len(snapshots) == 0 {
		return fmt.Errorf("no strippable mock-MSI role assignments found for principal %s at %s; strip would be a no-op", principalID, subscriptionScope)
	}
	return nil
}

func snapshotIfStrippableMockMSIAssignment(
	ra *armauthorization.RoleAssignment,
	subscriptionScope string,
	expectedPrincipalID string,
	roleNamesByID map[string]string,
) (MockMSIRoleAssignmentSnapshot, bool, error) {
	if ra == nil || ra.ID == nil || ra.Properties == nil || ra.Properties.RoleDefinitionID == nil || ra.Properties.Scope == nil {
		return MockMSIRoleAssignmentSnapshot{}, false, nil
	}
	if !strings.EqualFold(*ra.Properties.Scope, subscriptionScope) {
		return MockMSIRoleAssignmentSnapshot{}, false, nil
	}
	principalID := expectedPrincipalID
	if ra.Properties.PrincipalID != nil && *ra.Properties.PrincipalID != "" {
		if !strings.EqualFold(*ra.Properties.PrincipalID, expectedPrincipalID) {
			return MockMSIRoleAssignmentSnapshot{}, false, nil
		}
		principalID = *ra.Properties.PrincipalID
	}
	roleName := roleNamesByID[strings.ToLower(*ra.Properties.RoleDefinitionID)]
	if !isStrippableMockMSIRole(*ra.Properties.RoleDefinitionID, roleName) {
		return MockMSIRoleAssignmentSnapshot{}, false, nil
	}
	name := roleAssignmentNameFromID(*ra.ID)
	if name == "" {
		return MockMSIRoleAssignmentSnapshot{}, false, fmt.Errorf("mock-MSI role assignment %s has no assignment name", *ra.ID)
	}
	principalType := armauthorization.PrincipalTypeServicePrincipal
	if ra.Properties.PrincipalType != nil {
		principalType = *ra.Properties.PrincipalType
	}
	return MockMSIRoleAssignmentSnapshot{
		AssignmentID:     *ra.ID,
		Name:             name,
		Scope:            *ra.Properties.Scope,
		PrincipalID:      principalID,
		RoleDefinitionID: *ra.Properties.RoleDefinitionID,
		PrincipalType:    principalType,
	}, true, nil
}

func (tc *perItOrDescribeTestContext) mockMSIRoleAssignmentClients(ctx context.Context) (*armauthorization.RoleAssignmentsClient, *armauthorization.RoleDefinitionsClient, string, error) {
	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to get subscription ID: %w", err)
	}
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create role assignments client: %w", err)
	}
	roleDefsClient, err := armauthorization.NewRoleDefinitionsClient(creds, nil)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create role definitions client: %w", err)
	}
	return roleAssignmentsClient, roleDefsClient, subscriptionID, nil
}

// ListLeasedMockMSIPermissions lists the subscription-scoped mock-MSI grants
// that StripLeasedMockMSIPermissions would delete. Callers should register
// RestoreMockMSIPermissions (DeferCleanup) after a successful list and before
// any DeleteByID.
func (tc *perItOrDescribeTestContext) ListLeasedMockMSIPermissions(ctx context.Context) ([]MockMSIRoleAssignmentSnapshot, error) {
	startTime := time.Now()
	defer func() {
		tc.RecordTestStep("List leased mock-MSI permissions", startTime, time.Now())
	}()

	principalID, err := ResolveLeasedMockMSIPrincipalID()
	if err != nil {
		return nil, err
	}

	roleAssignmentsClient, roleDefsClient, subscriptionID, err := tc.mockMSIRoleAssignmentClients(ctx)
	if err != nil {
		return nil, err
	}

	subscriptionScope := fmt.Sprintf("/subscriptions/%s", subscriptionID)
	roleNamesByID, err := customRoleNamesByID(ctx, roleDefsClient, subscriptionScope)
	if err != nil {
		return nil, err
	}

	snapshots, err := listStrippableMockMSIAssignments(ctx, roleAssignmentsClient, subscriptionScope, principalID, roleNamesByID)
	if err != nil {
		return nil, err
	}
	if err := errIfNoStrippableMockMSIAssignments(snapshots, principalID, subscriptionScope); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// DeleteMockMSIRoleAssignments deletes previously listed mock-MSI grants.
func (tc *perItOrDescribeTestContext) DeleteMockMSIRoleAssignments(ctx context.Context, snapshots []MockMSIRoleAssignmentSnapshot) error {
	startTime := time.Now()
	defer func() {
		tc.RecordTestStep(fmt.Sprintf("Delete %d leased mock-MSI role assignments", len(snapshots)), startTime, time.Now())
	}()

	roleAssignmentsClient, _, _, err := tc.mockMSIRoleAssignmentClients(ctx)
	if err != nil {
		return err
	}

	for _, snapshot := range snapshots {
		ginkgo.GinkgoLogr.Info("stripping leased mock-MSI role assignment",
			"assignmentID", snapshot.AssignmentID,
			"principalID", snapshot.PrincipalID,
			"roleDefinitionID", snapshot.RoleDefinitionID)
		_, err := roleAssignmentsClient.DeleteByID(ctx, snapshot.AssignmentID, nil)
		if err != nil {
			if isRoleAssignmentNotFoundError(err) {
				continue
			}
			return fmt.Errorf("failed to delete mock-MSI role assignment %s: %w", snapshot.AssignmentID, err)
		}
	}
	return nil
}

// StripLeasedMockMSIPermissions deletes the subscription-scoped mock-MSI grants
// for this job's leased pool SP so cluster operations fail ACL checks the way
// they do in STG/PROD after customer UAMIs are gone. On a partial delete it
// restores already-removed assignments before returning. Callers that need to
// keep the grants stripped across later steps should List, register
// DeferCleanup restore, then Delete.
func (tc *perItOrDescribeTestContext) StripLeasedMockMSIPermissions(ctx context.Context) ([]MockMSIRoleAssignmentSnapshot, error) {
	startTime := time.Now()
	defer func() {
		tc.RecordTestStep("Strip leased mock-MSI permissions", startTime, time.Now())
	}()

	snapshots, err := tc.ListLeasedMockMSIPermissions(ctx)
	if err != nil {
		return snapshots, err
	}
	if err := tc.DeleteMockMSIRoleAssignments(ctx, snapshots); err != nil {
		restoreErr := tc.RestoreMockMSIPermissions(ctx, snapshots)
		return snapshots, errors.Join(err, restoreErr)
	}
	return snapshots, nil
}

func customRoleNamesByID(ctx context.Context, roleDefsClient *armauthorization.RoleDefinitionsClient, scope string) (map[string]string, error) {
	names := make(map[string]string)
	filter := "type eq 'CustomRole'"
	pager := roleDefsClient.NewListPager(scope, &armauthorization.RoleDefinitionsClientListOptions{
		Filter: &filter,
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list custom role definitions: %w", err)
		}
		for _, roleDef := range page.Value {
			if roleDef == nil || roleDef.ID == nil || roleDef.Properties == nil || roleDef.Properties.RoleName == nil {
				continue
			}
			names[strings.ToLower(*roleDef.ID)] = *roleDef.Properties.RoleName
		}
	}
	return names, nil
}

// mockMSIRoleAssignmentListFilter is the ARM $filter for listing this
// principal's assignments. Do not use `principalId eq '{id}'`: the
// authorization RP returns 400 UnsupportedQuery ("The filter 'principalId' is
// not supported") for that form when sent by the Go SDK. assignedTo() is the
// filter identities_helper uses. snapshotIfStrippableMockMSIAssignment still
// drops assignments whose Properties.PrincipalID does not match.
func mockMSIRoleAssignmentListFilter(principalID string) string {
	return fmt.Sprintf("assignedTo('%s')", principalID)
}

func listStrippableMockMSIAssignments(
	ctx context.Context,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	subscriptionScope string,
	principalID string,
	roleNamesByID map[string]string,
) ([]MockMSIRoleAssignmentSnapshot, error) {
	filter := mockMSIRoleAssignmentListFilter(principalID)
	pager := roleAssignmentsClient.NewListForScopePager(subscriptionScope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})

	var snapshots []MockMSIRoleAssignmentSnapshot
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list role assignments for mock-MSI principal %s: %w", principalID, err)
		}
		for _, ra := range page.Value {
			snapshot, ok, err := snapshotIfStrippableMockMSIAssignment(ra, subscriptionScope, principalID, roleNamesByID)
			if err != nil {
				return nil, err
			}
			if ok {
				snapshots = append(snapshots, snapshot)
			}
		}
	}
	return snapshots, nil
}

// RestoreMockMSIPermissions recreates previously stripped mock-MSI role
// assignments. ARM ErrorCode RoleAssignmentExists is treated as success so
// restore is idempotent if DeferCleanup races a privileged rbac re-apply.
// A generic HTTP 409 is GET-verified and retried: Create is retried when GET
// returns 404 (tombstone), not swallowed as success.
func (tc *perItOrDescribeTestContext) RestoreMockMSIPermissions(ctx context.Context, snapshots []MockMSIRoleAssignmentSnapshot) error {
	startTime := time.Now()
	defer func() {
		tc.RecordTestStep(fmt.Sprintf("Restore %d leased mock-MSI role assignments", len(snapshots)), startTime, time.Now())
	}()

	if len(snapshots) == 0 {
		return nil
	}

	roleAssignmentsClient, _, _, err := tc.mockMSIRoleAssignmentClients(ctx)
	if err != nil {
		return err
	}

	wait := func(d time.Duration) {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}

	var errs []error
	for _, snapshot := range snapshots {
		ginkgo.GinkgoLogr.Info("restoring leased mock-MSI role assignment",
			"assignmentID", snapshot.AssignmentID,
			"principalID", snapshot.PrincipalID)
		params := armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				PrincipalID:      &snapshot.PrincipalID,
				RoleDefinitionID: &snapshot.RoleDefinitionID,
				PrincipalType:    &snapshot.PrincipalType,
			},
		}
		create := func() error {
			_, err := roleAssignmentsClient.Create(ctx, snapshot.Scope, snapshot.Name, params, nil)
			return err
		}
		get := func() error {
			_, err := roleAssignmentsClient.GetByID(ctx, snapshot.AssignmentID, nil)
			return err
		}
		if err := restoreOneAssignmentWithWait(ctx, snapshot, create, get, wait); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
