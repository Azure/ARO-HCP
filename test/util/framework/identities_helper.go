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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const (
	UsePooledIdentitiesEnvvar = "POOLED_IDENTITIES"
	LeasedMSIContainersEnvvar = "LEASED_MSI_CONTAINERS"
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
	lockFile  *os.File
	statePath string
	entries   []leasedIdentityPoolEntry
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

func (state *leasedIdentityPoolState) lock() error {
	if err := syscall.Flock(int(state.lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return nil
}

func (state *leasedIdentityPoolState) unlock() error {
	if err := syscall.Flock(int(state.lockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("failed to release managed identities pool state file lock: %w", err)
	}
	return nil
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
