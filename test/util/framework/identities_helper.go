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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/pkg/filelock"
)

const (
	UsePooledIdentitiesEnvvar = "POOLED_IDENTITIES"
	LeasedMSIContainersEnvvar = "LEASED_MSI_CONTAINERS"
	E2ECustomRolePrefix       = "E2E-Test-CustomRole-"
)

// ErrNotEnoughFreeIdentityContainers is returned when a reservation request
// asks for more identity containers than are currently free in the pool.
var ErrNotEnoughFreeIdentityContainers = errors.New("not enough free identity containers")

// well-known MSI role names
const (
	ClusterApiAzureMiName        = "cluster-api-azure"
	ControlPlaneMiName           = "control-plane"
	CloudControllerManagerMiName = "cloud-controller-manager"
	IngressMiName                = "ingress"
	DiskCsiDriverMiName          = "disk-csi-driver"
	FileCsiDriverMiName          = "file-csi-driver"
	ImageRegistryMiName          = "image-registry"
	CloudNetworkConfigMiName     = "cloud-network-config"
	KmsMiName                    = "kms"
	DpDiskCsiDriverMiName        = "dp-disk-csi-driver"
	DpFileCsiDriverMiName        = "dp-file-csi-driver"
	DpImageRegistryMiName        = "dp-image-registry"
	ServiceManagedIdentityName   = "service"
)

type LeasedIdentityPool struct {
	ResourceGroupName string     `json:"resourceGroup"`
	Identities        Identities `json:"identities"`
}

type Identities struct {
	ClusterApiAzureMiName        string `json:"clusterApiAzureMiName"`
	ControlPlaneMiName           string `json:"controlPlaneMiName"`
	CloudControllerManagerMiName string `json:"cloudControllerManagerMiName"`
	IngressMiName                string `json:"ingressMiName"`
	DiskCsiDriverMiName          string `json:"diskCsiDriverMiName"`
	FileCsiDriverMiName          string `json:"fileCsiDriverMiName"`
	ImageRegistryMiName          string `json:"imageRegistryMiName"`
	CloudNetworkConfigMiName     string `json:"cloudNetworkConfigMiName"`
	KmsMiName                    string `json:"kmsMiName"`
	DpDiskCsiDriverMiName        string `json:"dpDiskCsiDriverMiName"`
	DpFileCsiDriverMiName        string `json:"dpFileCsiDriverMiName"`
	DpImageRegistryMiName        string `json:"dpImageRegistryMiName"`
	ServiceManagedIdentityName   string `json:"serviceManagedIdentityName"`
}

func (i Identities) ToSlice() []string {
	return []string{
		i.ClusterApiAzureMiName,
		i.ControlPlaneMiName,
		i.CloudControllerManagerMiName,
		i.IngressMiName,
		i.DiskCsiDriverMiName,
		i.FileCsiDriverMiName,
		i.ImageRegistryMiName,
		i.CloudNetworkConfigMiName,
		i.KmsMiName,
		i.DpDiskCsiDriverMiName,
		i.DpFileCsiDriverMiName,
		i.DpImageRegistryMiName,
		i.ServiceManagedIdentityName,
	}
}

func NewDefaultIdentities() Identities {
	return Identities{
		ClusterApiAzureMiName:        ClusterApiAzureMiName,
		ControlPlaneMiName:           ControlPlaneMiName,
		CloudControllerManagerMiName: CloudControllerManagerMiName,
		IngressMiName:                IngressMiName,
		DiskCsiDriverMiName:          DiskCsiDriverMiName,
		FileCsiDriverMiName:          FileCsiDriverMiName,
		ImageRegistryMiName:          ImageRegistryMiName,
		CloudNetworkConfigMiName:     CloudNetworkConfigMiName,
		KmsMiName:                    KmsMiName,
		DpDiskCsiDriverMiName:        DpDiskCsiDriverMiName,
		DpFileCsiDriverMiName:        DpFileCsiDriverMiName,
		DpImageRegistryMiName:        DpImageRegistryMiName,
		ServiceManagedIdentityName:   ServiceManagedIdentityName,
	}
}

func NewDefaultIdentitiesWithSuffix(suffix string) Identities {
	return Identities{
		ClusterApiAzureMiName:        fmt.Sprintf("%s-%s", ClusterApiAzureMiName, suffix),
		ControlPlaneMiName:           fmt.Sprintf("%s-%s", ControlPlaneMiName, suffix),
		CloudControllerManagerMiName: fmt.Sprintf("%s-%s", CloudControllerManagerMiName, suffix),
		IngressMiName:                fmt.Sprintf("%s-%s", IngressMiName, suffix),
		DiskCsiDriverMiName:          fmt.Sprintf("%s-%s", DiskCsiDriverMiName, suffix),
		FileCsiDriverMiName:          fmt.Sprintf("%s-%s", FileCsiDriverMiName, suffix),
		ImageRegistryMiName:          fmt.Sprintf("%s-%s", ImageRegistryMiName, suffix),
		CloudNetworkConfigMiName:     fmt.Sprintf("%s-%s", CloudNetworkConfigMiName, suffix),
		KmsMiName:                    fmt.Sprintf("%s-%s", KmsMiName, suffix),
		DpDiskCsiDriverMiName:        fmt.Sprintf("%s-%s", DpDiskCsiDriverMiName, suffix),
		DpFileCsiDriverMiName:        fmt.Sprintf("%s-%s", DpFileCsiDriverMiName, suffix),
		DpImageRegistryMiName:        fmt.Sprintf("%s-%s", DpImageRegistryMiName, suffix),
		ServiceManagedIdentityName:   fmt.Sprintf("%s-%s", ServiceManagedIdentityName, suffix),
	}
}

func (tc *perItOrDescribeTestContext) UsePooledIdentities() bool {
	return tc.perBinaryInvocationTestContext.UsePooledIdentities()
}

// ResolveIdentitiesForTemplate returns the identities object and the
// usePooledIdentities flag for parent Bicep templates which accept
// "identities" and "usePooledIdentities" parameters. This includes both
// templates invoked via CreateBicepTemplateAndWait and tests that call the ARM
// deployments client directly (e.g. BeginCreateOrUpdate) but still pass these
// two parameters into the template.
// In pooled mode it leases the next available identity container; otherwise it
// uses the provided resource group and well-known identity names.
func (tc *perItOrDescribeTestContext) ResolveIdentitiesForTemplate(resourceGroupName string) (LeasedIdentityPool, bool, error) {

	if !tc.UsePooledIdentities() {
		return LeasedIdentityPool{
			ResourceGroupName: resourceGroupName,
			Identities:        NewDefaultIdentities(),
		}, false, nil
	}

	leased, err := tc.getLeasedIdentities()
	if err != nil {
		return LeasedIdentityPool{}, false, err
	}

	return leased, true, nil
}

// DeployManagedIdentities runs the managed-identities.bicep module as a
// subscription-scoped deployment. It is used in tests which either:
//  1. Deploy managed-identities.json directly as a standalone deployment, or
//  2. Call CreateClusterCustomerResources, which orchestrates customer-infra
//     and then invokes this helper to configure managed identities.
//
// Parent Bicep templates (e.g. demo.json, cluster-only.json, etc.) that already
// wire the managed-identities module internally should not call this helper
// directly; instead they should use ResolveIdentitiesForTemplate to obtain the
// identities object and usePooledIdentities flag for their parameters.
func (tc *perItOrDescribeTestContext) DeployManagedIdentities(
	ctx context.Context,
	clusterName string,
	rbacScope RBACScope,
	opts ...BicepDeploymentOption,
) (*armresources.DeploymentExtended, error) {

	cfg := &bicepDeploymentConfig{
		scope:      BicepDeploymentScopeSubscription,
		timeout:    45 * time.Minute,
		parameters: map[string]interface{}{},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.scope == BicepDeploymentScopeResourceGroup {
		return nil, fmt.Errorf("DeployManagedIdentities cannot be called with BicepDeploymentScopeResourceGroup")
	}

	if cfg.deploymentName == "" {
		cfg.deploymentName = fmt.Sprintf("mi-%s", cfg.resourceGroup)
	}

	usePooled := tc.UsePooledIdentities()
	msiRGName := cfg.resourceGroup
	var identities Identities

	if usePooled {
		msiPool, err := tc.getLeasedIdentities()
		if err != nil {
			return nil, fmt.Errorf("failed to get leased MSIs: %w", err)
		}
		msiRGName = msiPool.ResourceGroupName
		identities = msiPool.Identities
	} else {
		identities = NewDefaultIdentitiesWithSuffix(clusterName)
	}

	parameters := map[string]interface{}{
		"nsgName":                  cfg.parameters["nsgName"],
		"vnetName":                 cfg.parameters["vnetName"],
		"subnetName":               cfg.parameters["subnetName"],
		"keyVaultName":             cfg.parameters["keyVaultName"],
		"useMsiPool":               usePooled,
		"clusterResourceGroupName": cfg.resourceGroup,
		"msiResourceGroupName":     msiRGName,
		"identities":               identities,
		"rbacScope":                rbacScope,
		"clusterName":              clusterName,
	}

	deploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
		WithTemplateFromBytes(cfg.template),
		WithScope(cfg.scope),
		WithDeploymentName(cfg.deploymentName),
		WithLocation(invocationContext().Location()),
		WithParameters(parameters),
		WithTimeout(cfg.timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed identities: %w", err)
	}

	return deploymentResult, nil
}

// AssignIdentityContainers attempts to assign n free identity containers to the caller by marking
// them as "assigned". It retries if there are fewer than n free entries until the context is done.
func (tc *perItOrDescribeTestContext) AssignIdentityContainers(ctx context.Context, count uint8, waitBetweenRetries time.Duration) error {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Assign %d identity containers", count), startTime, finishTime)
	}()

	state, err := tc.perBinaryInvocationTestContext.getLeasedIdentityPoolState()
	if err != nil {
		return fmt.Errorf("failed to open managed identities pool state file: %w", err)
	}

	for {
		err := state.assignNTo(specID(), count)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotEnoughFreeIdentityContainers) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitBetweenRetries):
		}
	}
}

// getLeasedIdentities returns the leased identities and container resource group by using one
// of the leases assigned to the calling test spec.
func (tc *perItOrDescribeTestContext) getLeasedIdentities() (LeasedIdentityPool, error) {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Lease identity container", startTime, finishTime)
	}()

	state, err := tc.perBinaryInvocationTestContext.getLeasedIdentityPoolState()
	if err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to open managed identities pool state file: %w", err)
	}

	leasedRG, err := state.useNextAssigned(specID())
	if err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to lease next managed identities resource group: %w", err)
	}

	return LeasedIdentityPool{
		ResourceGroupName: leasedRG,
		Identities:        NewDefaultIdentities(),
	}, nil
}

// leasedIdentityContainers returns the list of resource groups that are currently leased
// to the calling test spec.
func (tc *perItOrDescribeTestContext) leasedIdentityContainers() ([]string, error) {
	if !tc.UsePooledIdentities() {
		return nil, nil
	}

	state, err := tc.perBinaryInvocationTestContext.getLeasedIdentityPoolState()
	if err != nil {
		return nil, fmt.Errorf("failed to open managed identities pool state file: %w", err)
	}

	leasedContainers, err := state.getLeasedIdentityContainers(specID())
	if err != nil {
		return nil, fmt.Errorf("failed to get leased identity containers: %w", err)
	}
	return leasedContainers, nil
}

// releaseLeasedIdentities releases all the identity containers leased to the calling test spec.
// To be used only in the cleanup phase of the test.
func (tc *perItOrDescribeTestContext) releaseLeasedIdentities(ctx context.Context) error {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep("Release leased identities", startTime, finishTime)
	}()

	if !tc.UsePooledIdentities() {
		// For non-pooled mode, still clean up role assignments and custom role definitions
		subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
		if err != nil {
			return fmt.Errorf("failed to get subscription ID: %w", err)
		}

		// Clean up role assignments (role definitions are reusable across e2e tests)
		if err := tc.cleanupRoleAssignments(ctx, subscriptionID); err != nil {
			return fmt.Errorf("failed to cleanup role assignments: %w", err)
		}
		return nil
	}

	state, err := tc.perBinaryInvocationTestContext.getLeasedIdentityPoolState()
	if err != nil {
		return fmt.Errorf("failed to open managed identities pool state file: %w", err)
	}

	leasedContainers, err := state.getLeasedIdentityContainers(specID())
	if err != nil {
		return fmt.Errorf("failed to get leased identity containers: %w", err)
	}

	if len(leasedContainers) == 0 {
		return nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return err
	}
	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return err
	}
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return err
	}

	msiClientFactory, err := armmsi.NewClientFactory(subscriptionID, creds, nil)
	if err != nil {
		return err
	}
	ficsClient := msiClientFactory.NewFederatedIdentityCredentialsClient()

	var errs []error
	for _, resourceGroup := range leasedContainers {
		err := state.releaseByContainerName(resourceGroup,
			func() error {
				return tc.cleanupLeasedIdentityContainerFICs(ctx, ficsClient, resourceGroup)
			},
			func() error {
				return tc.cleanupLeasedIdentityContainerRoleAssignments(ctx, client, resourceGroup)
			},
		)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to release identity container %s: %w", resourceGroup, err))
		}
	}

	// Clean up role assignments (role definitions are reusable across e2e tests)
	if err := tc.cleanupRoleAssignments(ctx, subscriptionID); err != nil {
		errs = append(errs, fmt.Errorf("failed to cleanup role assignments: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed cleanup operations: %w", errors.Join(errs...))
	}
	return nil
}

// cleanupLeasedIdentityContainerFICs deletes all federated identity credentials contained in the identity container
// resource group.
func (tc *perItOrDescribeTestContext) cleanupLeasedIdentityContainerFICs(
	ctx context.Context,
	ficsClient *armmsi.FederatedIdentityCredentialsClient,
	resourceGroup string,
) error {

	identities := NewDefaultIdentities().ToSlice()

	wg := sync.WaitGroup{}
	errCh := make(chan error, len(identities))
	for _, identityName := range identities {
		wg.Add(1)
		go func(ctx context.Context, identityName string) {
			defer wg.Done()

			var errs []error

			pager := ficsClient.NewListPager(resourceGroup, identityName, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to list FICs for identity %q in resource group %q: %w", identityName, resourceGroup, err))
					break
				}

				for _, fic := range page.Value {
					_, err := ficsClient.Delete(ctx, resourceGroup, identityName, *fic.Name, nil)
					if err != nil {
						var respErr *azcore.ResponseError
						if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
							continue
						}
						errs = append(errs, fmt.Errorf("failed to delete FIC %q in resource group %q: %w", *fic.Name, resourceGroup, err))
					}
				}
			}

			if len(errs) > 0 {
				errCh <- fmt.Errorf("failed to cleanup FICs for identity: %w", errors.Join(errs...))
			}
		}(ctx, identityName)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed cleanup operations: %w", errors.Join(errs...))
	}
	return nil
}

// trackRoleAssignment tracks a role assignment ID for cleanup.
func (tc *perItOrDescribeTestContext) trackRoleAssignment(assignmentID string) {
	tc.contextLock.Lock()
	defer tc.contextLock.Unlock()

	// Check if already tracked to avoid duplicates
	for _, id := range tc.createdRoleAssignmentIDs {
		if id == assignmentID {
			return
		}
	}

	tc.createdRoleAssignmentIDs = append(tc.createdRoleAssignmentIDs, assignmentID)
}

// cleanupRoleAssignments deletes only the role assignments created by THIS test.
// Role definitions are not cleaned up - they are reusable across e2e test runs.
func (tc *perItOrDescribeTestContext) cleanupRoleAssignments(ctx context.Context, subscriptionID string) error {
	tc.contextLock.RLock()
	assignmentIDsToDelete := make([]string, len(tc.createdRoleAssignmentIDs))
	copy(assignmentIDsToDelete, tc.createdRoleAssignmentIDs)
	tc.contextLock.RUnlock()

	if len(assignmentIDsToDelete) == 0 {
		ginkgo.GinkgoLogr.Info("No role assignments to clean up")
		return nil
	}

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return err
	}

	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	// Delete only the role assignments we created
	var errs []error
	for _, assignmentID := range assignmentIDsToDelete {
		ginkgo.GinkgoLogr.Info("Deleting role assignment created by this test",
			"assignmentID", assignmentID)

		_, err := roleAssignmentsClient.DeleteByID(ctx, assignmentID, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				ginkgo.GinkgoLogr.Info("Role assignment already deleted", "assignmentID", assignmentID)
				continue
			}
			errs = append(errs, fmt.Errorf("failed to delete role assignment %s: %w", assignmentID, err))
		} else {
			ginkgo.GinkgoLogr.Info("Successfully deleted role assignment", "assignmentID", assignmentID)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete some role assignments: %w", errors.Join(errs...))
	}

	return nil
}

// cleanupCustomE2ETestRolesAssignmentsForIdentity finds all role assignments
// for the given identity that reference E2E custom role definitions (matching
// the E2ECustomRolePrefix) and deletes those role assignments.
func (tc *perItOrDescribeTestContext) cleanupCustomE2ETestRolesAssignmentsForIdentity(
	ctx context.Context,
	principalID string,
) error {

	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to get subscription ID: %w", err)
	}

	ginkgo.GinkgoLogr.Info("Cleanup role assignments to E2E custom roles",
		"subscriptionID", subscriptionID,
		"roleNamePrefix", E2ECustomRolePrefix,
		"principalID", principalID)

	// List all role assignments for this principal at subscription scope
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	roleDefsClient, err := armauthorization.NewRoleDefinitionsClient(creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role definitions client: %w", err)
	}

	subscriptionScope := fmt.Sprintf("/subscriptions/%s", subscriptionID)

	// Pre-fetch all custom role definitions into a map (role definition ID -> role name)
	// to avoid N+1 API calls when checking each role assignment.
	e2eCustomRoles := make(map[string]string)
	roleDefFilter := "type eq 'CustomRole'"
	roleDefPager := roleDefsClient.NewListPager(subscriptionScope, &armauthorization.RoleDefinitionsClientListOptions{
		Filter: &roleDefFilter,
	})
	for roleDefPager.More() {
		page, err := roleDefPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list custom role definitions: %w", err)
		}
		for _, roleDef := range page.Value {
			if roleDef.Properties == nil || roleDef.Properties.RoleName == nil || roleDef.ID == nil {
				continue
			}
			if strings.HasPrefix(*roleDef.Properties.RoleName, E2ECustomRolePrefix) {
				e2eCustomRoles[strings.ToLower(*roleDef.ID)] = *roleDef.Properties.RoleName
			}
		}
	}

	if len(e2eCustomRoles) == 0 {
		return nil
	}

	// List all role assignments for the principal using assignedTo() filter.
	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := roleAssignmentsClient.NewListForScopePager(subscriptionScope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})

	var errs []error
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list role assignments for principal %s: %w", principalID, err)
		}

		for _, ra := range page.Value {
			if ra.Properties == nil || ra.Properties.RoleDefinitionID == nil {
				continue
			}

			// Only consider assignments scoped exactly at the subscription level
			if !strings.EqualFold(*ra.Properties.Scope, subscriptionScope) {
				continue
			}

			roleName, isE2ERole := e2eCustomRoles[strings.ToLower(*ra.Properties.RoleDefinitionID)]
			if !isE2ERole {
				continue
			}

			ginkgo.GinkgoLogr.Info("Deleting role assignment to E2E custom role",
				"assignmentID", *ra.ID,
				"roleName", roleName,
				"principalID", principalID)

			_, err = roleAssignmentsClient.DeleteByID(ctx, *ra.ID, nil)
			if err != nil {
				var respErr *azcore.ResponseError
				if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to delete role assignment %s: %w", *ra.ID, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to fully clean up E2E custom role assignments for identity %s: %w", principalID, errors.Join(errs...))
	}

	return nil
}

// cleanupLeasedIdentityContainerRoleAssignments cleans up the identity container by deleting all the role assignments
// that were created within it.
func (tc *perItOrDescribeTestContext) cleanupLeasedIdentityContainerRoleAssignments(ctx context.Context,
	client *armauthorization.RoleAssignmentsClient, resourceGroup string) error {

	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return err
	}

	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroup)

	var toDelete []*armauthorization.RoleAssignment
	pager := client.NewListForResourceGroupPager(resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list role assignments for scope %s: %w", scope, err)
		}
		for _, ra := range page.Value {
			if !strings.HasPrefix(strings.ToLower(*ra.Properties.Scope), strings.ToLower(scope)) {
				continue
			}
			toDelete = append(toDelete, ra)
		}
	}

	if len(toDelete) == 0 {
		return nil
	}

	wg := sync.WaitGroup{}
	errCh := make(chan error, len(toDelete))

	for _, ra := range toDelete {
		wg.Add(1)
		go func(ctx context.Context, ra *armauthorization.RoleAssignment) {
			defer wg.Done()

			_, err := client.Delete(ctx, *ra.Properties.Scope, *ra.Name, nil)
			if err != nil {
				var respErr *azcore.ResponseError
				if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
					return
				}
				errCh <- fmt.Errorf("failed to delete role assignment %s: %w", *ra.ID, err)
			}
		}(ctx, ra)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed cleanup operations: %w", errors.Join(errs...))
	}
	return nil
}

type leaseState string

const (
	leaseStateFree     leaseState = "free"
	leaseStateAssigned leaseState = "assigned"
	leaseStateBusy     leaseState = "busy"
)

type leaseEntry struct {
	State          leaseState `json:"state"`
	LeasedBy       string     `json:"leasedBy,omitempty"`
	TransitionedAt string     `json:"transitionedAt,omitempty"`
}

type leasedIdentityPoolEntry struct {
	ResourceGroup string       `json:"resourceGroup"`
	Current       leaseEntry   `json:"current"`
	History       []leaseEntry `json:"history,omitempty"`
}

func (e *leasedIdentityPoolEntry) isFree() bool {
	return e.Current.State == leaseStateFree
}

func (e *leasedIdentityPoolEntry) isAssignedTo(me string) bool {
	return e.Current.State == leaseStateAssigned && e.Current.LeasedBy == me
}

func (e *leasedIdentityPoolEntry) isBusy() bool {
	return e.Current.State == leaseStateBusy
}

func (e *leasedIdentityPoolEntry) assignTo(me string) error {
	if !e.isFree() {
		return errors.New("not free")
	}
	e.History = append(e.History, e.Current)
	e.Current.State = leaseStateAssigned
	e.Current.LeasedBy = me
	e.Current.TransitionedAt = time.Now().UTC().Format(time.RFC3339)

	return nil
}

func (e *leasedIdentityPoolEntry) use(me string) error {
	if !e.isAssignedTo(me) || e.isBusy() {
		return errors.New("not assigned to me or already busy")
	}
	e.History = append(e.History, e.Current)
	e.Current.State = leaseStateBusy
	e.Current.TransitionedAt = time.Now().UTC().Format(time.RFC3339)

	return nil
}

func (e *leasedIdentityPoolEntry) release(cleanups ...func() error) error {
	if e.Current.State == leaseStateFree {
		return nil
	}
	e.History = append(e.History, e.Current)
	e.Current.State = leaseStateFree
	e.Current.LeasedBy = ""
	e.Current.TransitionedAt = time.Now().UTC().Format(time.RFC3339)

	errs := []error{}
	for _, cleanup := range cleanups {
		if err := cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed cleanup operations: %w", errors.Join(errs...))
	}
	return nil
}

type leasedIdentityPoolState struct {
	// lockFile ensures single process access to the state file.
	lockFile *os.File
	// mu ensures single thread access to the state file to avoid
	// intra-test parallelism issues.
	mu sync.Mutex
	// statePath is the path to the state file.
	statePath string
	// entries is the list of leased identity pool entries.
	entries []leasedIdentityPoolEntry
}

// newLeasedIdentityPoolState creates a new leased identity pool state.
func newLeasedIdentityPoolState(path string) (*leasedIdentityPoolState, error) {

	lockFilePath := filepath.Join(os.TempDir(), "identities-pool-state.lock")

	lf, err := os.OpenFile(lockFilePath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return &leasedIdentityPoolState{}, fmt.Errorf("failed to open managed identities pool state lock file: %w", err)
	}

	state := leasedIdentityPoolState{statePath: path, lockFile: lf}

	if err := state.lock(); err != nil {
		return &leasedIdentityPoolState{}, fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			ginkgo.GinkgoLogr.Info("WARN: failed to release managed identities pool state file lock", "error", err)
		}
	}()

	if err := state.readUnlocked(); err != nil {
		return &leasedIdentityPoolState{}, fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	if state.isInitialized() {
		return &state, nil
	}

	leasedRGs := strings.Fields(strings.TrimSpace(os.Getenv(LeasedMSIContainersEnvvar)))
	if len(leasedRGs) == 0 {
		return &leasedIdentityPoolState{}, fmt.Errorf("expected envvar %s to not be empty", LeasedMSIContainersEnvvar)
	}

	if err := state.initializeUnlocked(leasedRGs); err != nil {
		return &leasedIdentityPoolState{}, fmt.Errorf("failed to initialize managed identities pool state: %w", err)
	}
	ginkgo.GinkgoLogr.Info("initialized managed identities pool state", "entries", len(state.entries))

	return &state, nil
}

// useNextAssigned uses the next "assigned" identity container for the caller.
func (state *leasedIdentityPoolState) useNextAssigned(me string) (string, error) {
	if err := state.lock(); err != nil {
		return "", fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			ginkgo.GinkgoLogr.Info("WARN: failed to release managed identities pool state file lock", "error", err)
		}
	}()

	err := state.readUnlocked()
	if err != nil {
		return "", fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	var leasedRG string
	for i := range state.entries {

		if err := state.entries[i].use(me); err != nil {
			continue
		}

		leasedRG = state.entries[i].ResourceGroup
		break
	}

	if leasedRG == "" {
		return "", fmt.Errorf("no assigned identity containers available for %s", me)
	}

	if err = state.writeUnlocked(); err != nil {
		return "", fmt.Errorf("failed to write managed identities pool state file: %w", err)
	}

	return leasedRG, nil
}

// assignNTo attempts to assign n free identity containers to the caller by marking
// them as "assigned". It does not perform any waiting or retries: if there are
// fewer than n free entries, it returns an error and leaves the state
// unchanged.
func (state *leasedIdentityPoolState) assignNTo(me string, n uint8) error {
	if err := state.lock(); err != nil {
		return fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			ginkgo.GinkgoLogr.Info("WARN: failed to release managed identities pool state file lock", "error", err)
		}
	}()

	if err := state.readUnlocked(); err != nil {
		return fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	count := 0
	for i := range state.entries {
		if err := state.entries[i].assignTo(me); err != nil {
			continue
		}
		count++
		if count == int(n) {
			break
		}
	}

	if count < int(n) {
		// return and don't persist the partial in-memory state to file
		return fmt.Errorf("%w: requested %d identity containers but only %d are assigned", ErrNotEnoughFreeIdentityContainers, n, count)
	}

	if err := state.writeUnlocked(); err != nil {
		return fmt.Errorf("failed to write managed identities pool state file: %w", err)
	}

	return nil
}

// releaseByContainerName releases the identity container by the given name.
func (state *leasedIdentityPoolState) releaseByContainerName(resourceGroup string, cleanupFn ...func() error) error {
	if err := state.lock(); err != nil {
		return fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			ginkgo.GinkgoLogr.Info("WARN: failed to release managed identities pool state file lock", "error", err)
		}
	}()

	err := state.readUnlocked()
	if err != nil {
		return fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}
	for i := range state.entries {
		if state.entries[i].ResourceGroup == resourceGroup {
			if err := state.entries[i].release(cleanupFn...); err != nil {
				// cleanup is best effort, just log errors and continue
				ginkgo.GinkgoLogr.Info("WARN: failed to release managed identities resource group", "resourceGroup", resourceGroup, "error", err)
			}
			if err := state.writeUnlocked(); err != nil {
				return fmt.Errorf("failed to write managed identities pool state file: %w", err)
			}
			break
		}
	}

	return nil
}

// getLeasedIdentityContainers returns the list of resource groups that are currently leased
// to the caller.
func (state *leasedIdentityPoolState) getLeasedIdentityContainers(me string) ([]string, error) {
	if err := state.lock(); err != nil {
		return nil, fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			ginkgo.GinkgoLogr.Info("WARN: failed to release managed identities pool state file lock", "error", err)
		}
	}()

	if err := state.readUnlocked(); err != nil {
		return nil, fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	resourceGroups := make([]string, 0, len(state.entries))
	for _, entry := range state.entries {
		if entry.Current.LeasedBy == me {
			resourceGroups = append(resourceGroups, entry.ResourceGroup)
		}
	}
	return resourceGroups, nil
}

func (state *leasedIdentityPoolState) readUnlocked() error {

	f, err := os.OpenFile(state.statePath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return fmt.Errorf("failed to open managed identities pool state file %s: %w", state.statePath, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			ginkgo.GinkgoLogr.Info("WARN: failed to close managed identities pool state file after read", "error", err)
		}
	}()

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of managed identities pool state file: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	var entries []leasedIdentityPoolEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to unmarshal managed identities pool state file: %w", err)
	}
	state.entries = entries

	return nil
}

func (state *leasedIdentityPoolState) writeUnlocked() error {
	updated, err := yaml.Marshal(state.entries)
	if err != nil {
		return fmt.Errorf("failed to marshal updated managed identities pool state: %w", err)
	}

	dir := filepath.Dir(state.statePath)
	tmp, err := os.CreateTemp(dir, "identities-pool-state-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary managed identities pool state file: %w", err)
	}

	cleanupTemp := func() {
		if err := os.Remove(tmp.Name()); err != nil && !os.IsNotExist(err) {
			ginkgo.GinkgoLogr.Info("WARN: failed to remove temporary managed identities pool state file", "error", err)
		}
	}

	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		cleanupTemp()
		return fmt.Errorf("failed to write temporary managed identities pool state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanupTemp()
		return fmt.Errorf("failed to sync temporary managed identities pool state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTemp()
		return fmt.Errorf("failed to close temporary managed identities pool state file: %w", err)
	}

	if err := os.Rename(tmp.Name(), state.statePath); err != nil {
		cleanupTemp()
		return fmt.Errorf("failed to replace managed identities pool state file: %w", err)
	}

	return nil
}

func (state *leasedIdentityPoolState) isInitialized() bool {
	return len(state.entries) > 0
}

func (state *leasedIdentityPoolState) initializeUnlocked(leasedRGs []string) error {
	entries := make([]leasedIdentityPoolEntry, 0, len(leasedRGs))
	for _, rg := range leasedRGs {
		entries = append(entries, leasedIdentityPoolEntry{
			ResourceGroup: rg,
			Current: leaseEntry{
				State:          leaseStateFree,
				TransitionedAt: time.Now().UTC().Format(time.RFC3339),
			},
		})
	}
	state.entries = entries
	if err := state.writeUnlocked(); err != nil {
		return fmt.Errorf("failed to write managed identities pool state file: %w", err)
	}

	return nil
}

func specID() string {
	return fmt.Sprintf("%s|pid:%d", strings.Join(strings.Fields(ginkgo.CurrentSpecReport().FullText()), "-"), os.Getpid())
}

func (state *leasedIdentityPoolState) lock() error {
	state.mu.Lock()
	return filelock.Lock(state.lockFile.Fd())
}

func (state *leasedIdentityPoolState) unlock() error {
	err := filelock.Unlock(state.lockFile.Fd())
	state.mu.Unlock()
	return err
}

// Azure Red Hat OpenShift Hosted Control Planes Service Managed Identity built-in role
const ServiceManagedIdentityBuiltInRoleID = "c0ff367d-66d8-445e-917c-583feb0ef0d4"

// IdentityRoleAssignments defines the expected role assignments for a managed identity.
type IdentityRoleAssignments struct {
	// BuiltInRoleDefinitionID is the Azure built-in role that should be assigned to this identity
	BuiltInRoleDefinitionID string
	// RequiredActions is the complete list of all RBAC actions the identity needs
	// The validation will check if the built-in role provides these, and create a custom role
	// with any missing actions
	RequiredActions []string
}

// GetExpectedDefinitions returns the expected permissions for a given identity type.
// The permissions are derived from roles defined in test/e2e-setup/bicep/modules/managed-identities.bicep
// These are the actual actions that the role grants, fetched from Azure role definitions.
//
// The actions returned for build-in roles can deviate from the the ones that are actually present
// in Azure. This is legit and we use it at times where we need to test new permissions before the
// build-in role is rolled out to Azure.
func GetExpectedDefinitions(identityType string) (*IdentityRoleAssignments, error) {
	switch identityType {
	case ServiceManagedIdentityName:
		return &IdentityRoleAssignments{
			// Azure Red Hat OpenShift Hosted Control Planes Service Managed Identity
			BuiltInRoleDefinitionID: ServiceManagedIdentityBuiltInRoleID,
			RequiredActions: []string{
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/read",
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/write",
				"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/delete",
				"Microsoft.ManagedIdentity/userAssignedIdentities/read",
				"Microsoft.Network/loadBalancers/backendAddressPools/read",  // read backend address pools of LB to check if the backend address pool already exists
				"Microsoft.Network/loadBalancers/backendAddressPools/write", // write backend address pools to LB
				"Microsoft.Network/loadBalancers/read",                      // to check if LB exists or not before writing to it
				"Microsoft.Network/loadBalancers/write",                     // create LB if it doesn't exist
				"Microsoft.Network/natGateways/join/action",                 // subnet/write needs /join/action on nat gateway if present in request
				"Microsoft.Network/natGateways/read",
				"Microsoft.Network/networkSecurityGroups/join/action", // subnet/write needs /join/action on NSG if present in request
				"Microsoft.Network/networkSecurityGroups/read",        // validate NSG
				"Microsoft.Network/networkSecurityGroups/write",
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks/read",  // read existing links between private DNS zone and virtual network
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks/write", // attach private DNS zone to virtual network
				"Microsoft.Network/routeTables/join/action",                   // subnet/write needs /join/action on nat route table if present in request
				"Microsoft.Network/routeTables/read",
				"Microsoft.Network/virtualNetworks/join/action",             // attach private DNS zone
				"Microsoft.Network/virtualNetworks/joinLoadBalancer/action", // add private IP addresses to LB backend
				"Microsoft.Network/virtualNetworks/read",                    // validate CIDR & existence
				"Microsoft.Network/virtualNetworks/subnets/join/action",     // create private load balancer and join to subnet
				"Microsoft.Network/virtualNetworks/subnets/read",            // validate CIDR & existence
				"Microsoft.Network/virtualNetworks/subnets/write",           // attach the NSG to subnet
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown identity type: %s", identityType)
	}
}

// EnsureIdentityRoleAssignments validates that a managed identity has the built-in role assigned
// and creates/assigns a custom role with any missing permissions.
//
// This simplified approach:
// 1. Checks if the built-in role (c0ff367d-66d8-445e-917c-583feb0ef0d4) is assigned at target RG
// 2. Gets the actions from that built-in role
// 3. Compares with RequiredActions to find missing permissions
// 4. Creates a custom role with ONLY the missing actions (with predictable hash)
// 5. Assigns it to the identity
//
// Parameters:
//   - identityName: name of the managed identity to validate
//   - identityResourceGroup: resource group where the identity lives (may be pooled MSI RG)
func (tc *perItOrDescribeTestContext) EnsureIdentityRoleAssignments(
	ctx context.Context,
	identityType string,
	identityName string,
	identityResourceGroup string,
) error {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Validate role bindings for %s identity %s", identityType, identityName), startTime, finishTime)
	}()

	// Get expected role configuration for this identity
	expectedAssignments, err := GetExpectedDefinitions(identityType)
	if err != nil {
		return fmt.Errorf("failed to get expected role bindings: %w", err)
	}

	if len(expectedAssignments.RequiredActions) == 0 {
		// No permissions expected for this identity
		return nil
	}

	// Get Azure credentials and subscription ID
	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to get subscription ID: %w", err)
	}

	// Get the managed identity
	msiClientFactory, err := armmsi.NewClientFactory(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create MSI client factory: %w", err)
	}

	identity, err := msiClientFactory.NewUserAssignedIdentitiesClient().Get(ctx, identityResourceGroup, identityName, nil)
	if err != nil {
		return fmt.Errorf("failed to get managed identity %s in resource group %s: %w", identityName, identityResourceGroup, err)
	}

	roleDefinitionsClient, err := armauthorization.NewRoleDefinitionsClient(creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role definitions client: %w", err)
	}

	// Cleanup any existing E2E custom role assignments for this identity
	err = tc.cleanupCustomE2ETestRolesAssignmentsForIdentity(ctx, *identity.Properties.PrincipalID)
	if err != nil {
		return fmt.Errorf("failed to cleanup custom E2E test roles assignments for identity %s: %w", identityName, err)
	}

	// Get the built-in role definition to see what actions it provides
	builtInRoleID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
		subscriptionID, expectedAssignments.BuiltInRoleDefinitionID)

	ginkgo.GinkgoLogr.Info("Fetching built-in role definition",
		"identityName", identityName,
		"builtInRoleID", expectedAssignments.BuiltInRoleDefinitionID)

	builtInRole, err := roleDefinitionsClient.GetByID(ctx, builtInRoleID, nil)
	if err != nil {
		return fmt.Errorf("failed to get built-in role definition %s: %w", builtInRoleID, err)
	}

	// Extract actions from the built-in role
	grantedActions := make(map[string]bool)
	if builtInRole.Properties != nil && builtInRole.Properties.Permissions != nil {
		for _, permission := range builtInRole.Properties.Permissions {
			if permission.Actions != nil {
				for _, action := range permission.Actions {
					if action != nil {
						grantedActions[*action] = true
					}
				}
			}
		}
	}

	ginkgo.GinkgoLogr.Info("Built-in role actions retrieved",
		"identityName", identityName,
		"actionsCount", len(grantedActions))

	// Check what actions are missing from the built-in role
	missingActions := checkMissingPermissions(expectedAssignments.RequiredActions, grantedActions)

	if len(missingActions) > 0 {
		ginkgo.GinkgoLogr.Info("Identity is missing required permissions, use custom role",
			"identity", identityName,
			"missingActions", missingActions)

		// Ensure a custom role with the missing actions exists
		// Role is created at subscription scope but assigned at target RG
		customRoleNamePrefix := E2ECustomRolePrefix + identityType
		customRoleID, customRoleName, err := tc.ensureCustomRole(ctx, subscriptionID, customRoleNamePrefix, missingActions)
		if err != nil {
			return fmt.Errorf("failed to create custom role for identity %s: %w", identityName, err)
		}

		// Assign the custom role to the identity at subscription scope
		err = tc.assignRoleToIdentity(ctx, subscriptionID, *identity.Properties.PrincipalID, customRoleID)
		if err != nil {
			return fmt.Errorf("failed to assign custom role to identity %s: %w", identityName, err)
		}

		ginkgo.GinkgoLogr.Info("Custom role created and assigned successfully",
			"identity", identityName,
			"customRoleName", customRoleName,
			"customRoleID", customRoleID)
	} else {
		ginkgo.GinkgoLogr.Info("Identity permissions validated successfully",
			"identity", identityName,
			"grantedActions", len(grantedActions))
	}

	return nil
}

// checkMissingPermissions checks if all expected permissions are granted.
func checkMissingPermissions(expected []string, granted map[string]bool) []string {
	var missing []string

	for _, expectedPerm := range expected {
		if !hasPermission(expectedPerm, granted) {
			missing = append(missing, expectedPerm)
		}
	}

	return missing
}

// hasPermission checks if a specific permission is granted
func hasPermission(required string, granted map[string]bool) bool {
	// Direct match
	if granted[required] {
		return true
	}

	return false
}

// ensureCustomRole ensures a custom Azure role definition with the specified actions exists.
// The role is scoped to the subscription and can be assigned at the resource group level.
// Each unique set of actions gets its own role definition.
func (tc *perItOrDescribeTestContext) ensureCustomRole(
	ctx context.Context,
	subscriptionID string,
	roleNamePrefix string,
	actions []string,
) (string, string, error) {
	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return "", "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	roleDefsClient, err := armauthorization.NewRoleDefinitionsClient(creds, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create role definitions client: %w", err)
	}

	// Generate a unique role definition ID based on subscription, role name, and the specific actions
	// This ensures each unique set of missing permissions gets its own role definition
	sorted := slices.Sorted(slices.Values(actions))
	sorted = slices.Compact(sorted)
	roleDefID := guid(subscriptionID, roleNamePrefix, strings.Join(sorted, "|"))

	roleName := fmt.Sprintf("%s-%s", roleNamePrefix, roleDefID)

	scope := fmt.Sprintf("/subscriptions/%s", subscriptionID)

	// Check if this exact role already exists
	existingRole, err := roleDefsClient.Get(ctx, scope, roleDefID, nil)
	if err == nil && existingRole.Properties != nil {
		// Role with this exact set of actions already exists from a previous test run
		// Do NOT track it for cleanup - we only clean up roles created in THIS test run
		ginkgo.GinkgoLogr.Info("Custom role with these actions already exists, reusing it",
			"roleName", roleName,
			"roleID", *existingRole.ID,
			"actionCount", len(actions))
		return *existingRole.ID, *existingRole.Properties.RoleName, nil
	}

	// If Get failed with something other than 404, return the error
	if err != nil {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
			return "", "", fmt.Errorf("failed to check if role definition exists: %w", err)
		}
		// 404 means role doesn't exist, proceed to create it
	}

	// Create new role definition with the missing actions
	// Set AssignableScopes to subscription level because of our deny assignments on MRGs
	roleProperties := &armauthorization.RoleDefinitionProperties{
		RoleName:    &roleName,
		Description: to.Ptr(fmt.Sprintf("E2E test custom role for %s with additional permissions", roleName)),
		RoleType:    to.Ptr("CustomRole"),
		Permissions: []*armauthorization.Permission{
			{
				Actions:    to.SliceOfPtrs(sorted...),
				NotActions: []*string{},
			},
		},
		AssignableScopes: []*string{
			to.Ptr(scope), // subscription scope
		},
	}

	roleDefinition := armauthorization.RoleDefinition{
		Properties: roleProperties,
	}

	result, err := roleDefsClient.CreateOrUpdate(ctx, scope, roleDefID, roleDefinition, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create role definition: %w", err)
	}

	ginkgo.GinkgoLogr.Info("Custom role created with missing actions (reusable across e2e tests)",
		"roleName", roleName,
		"roleID", *result.ID,
		"roleDefID", roleDefID,
		"actions", actions)

	return *result.ID, *result.Properties.RoleName, nil
}

// assignRoleToIdentity assigns a role to a managed identity at the subscription scope.
func (tc *perItOrDescribeTestContext) assignRoleToIdentity(
	ctx context.Context,
	subscriptionID string,
	principalID string,
	roleDefinitionID string,
) error {
	creds, err := tc.perBinaryInvocationTestContext.getAzureCredentials()
	if err != nil {
		return fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	// Generate a unique assignment name using GUID
	// Assign at subscription scope to match where the built-in role is assigned
	scope := fmt.Sprintf("/subscriptions/%s", subscriptionID)
	assignmentName := guid(scope, principalID, roleDefinitionID)

	roleAssignmentProperties := &armauthorization.RoleAssignmentProperties{
		PrincipalID:      &principalID,
		RoleDefinitionID: &roleDefinitionID,
		PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
	}

	roleAssignment := armauthorization.RoleAssignmentCreateParameters{
		Properties: roleAssignmentProperties,
	}

	result, err := roleAssignmentsClient.Create(ctx, scope, assignmentName, roleAssignment, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignment: %w", err)
	}

	// Track this role assignment so we can clean it up later
	tc.trackRoleAssignment(*result.ID)

	ginkgo.GinkgoLogr.Info("Role assigned to identity",
		"scope", scope,
		"principalID", principalID,
		"roleDefinitionID", roleDefinitionID,
		"assignmentID", *result.ID)

	return nil
}

// guid generates a deterministic UUID for Azure resource names.
// This matches the pattern used in Bicep: guid(scope, principal, role).
// Uses UUID v5 (SHA-1 based) for RFC 4122 compliant deterministic UUIDs.
func guid(parts ...string) string {
	combined := strings.Join(parts, "|")
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(combined)).String()
}
