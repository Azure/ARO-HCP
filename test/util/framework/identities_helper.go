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
	"strings"
	"syscall"
	"time"

	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/log"
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

	var msiRGName string
	var identities Identities

	if usePooled {
		msiPool, err := tc.getLeasedIdentities()
		if err != nil {
			return nil, fmt.Errorf("failed to get leased MSIs: %w", err)
		}
		msiRGName = msiPool.ResourceGroupName
		identities = msiPool.Identities
	} else {
		msiRGName = cfg.resourceGroup
		identities = NewDefaultIdentities()
	}

	parameters := map[string]interface{}{
		"clusterResourceGroupName": cfg.resourceGroup,
		"msiResourceGroupName":     msiRGName,
		"useMsiPool":               usePooled,
		"identities":               identities,
		"nsgName":                  cfg.parameters["nsgName"],
		"vnetName":                 cfg.parameters["vnetName"],
		"subnetName":               cfg.parameters["subnetName"],
		"keyVaultName":             cfg.parameters["keyVaultName"],
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

func (tc *perItOrDescribeTestContext) AssignIdentityContainers(ctx context.Context, count uint8, waitBetweenRetries time.Duration) error {
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

func (tc *perItOrDescribeTestContext) getLeasedIdentities() (LeasedIdentityPool, error) {

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

func (tc *perItOrDescribeTestContext) releaseLeasedIdentities(ctx context.Context) error {
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

	var errs []error
	for _, resourceGroup := range leasedContainers {
		err := state.releaseByContainerName(resourceGroup, func() error {
			return tc.cleanupLeasedIdentityContainer(ctx, client, resourceGroup)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to release identity container %s: %w", resourceGroup, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed cleanup operations: %w", errors.Join(errs...))
	}
	return nil
}

// func (tc *perItOrDescribeTestContext) registerLeasedIdentityContainer(resourceGroupName string) error {
// 	tc.contextLock.Lock()
// 	defer tc.contextLock.Unlock()
// 	tc.knownLeasedIdentityContainers = append(tc.knownLeasedIdentityContainers, resourceGroupName)
// 	return nil
// }

func (tc *perItOrDescribeTestContext) cleanupLeasedIdentityContainer(ctx context.Context,
	client *armauthorization.RoleAssignmentsClient, resourceGroup string) error {

	subscriptionID, err := tc.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return err
	}

	scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroup)

	var errs []error
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

			_, err := client.Delete(ctx, *ra.Properties.Scope, *ra.Name, nil)
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
		return fmt.Errorf("failed cleanup operations: %w", errors.Join(errs...))
	}
	return nil
}

type LeaseState string

const (
	LeaseStateFree     LeaseState = "free"
	LeaseStateAssigned LeaseState = "assigned"
	LeaseStateBusy     LeaseState = "busy"
)

type LeaseEntry struct {
	State          LeaseState `yaml:"state"`
	LeasedBy       string     `yaml:"leasedBy,omitempty"`
	TransitionedAt string     `yaml:"transitionedAt,omitempty"`
}

type LeasedIdentityPoolEntry struct {
	ResourceGroup string       `yaml:"resourceGroup"`
	Current       LeaseEntry   `yaml:"current"`
	History       []LeaseEntry `yaml:"history"`
}

func (e *LeasedIdentityPoolEntry) isFree() bool {
	return e.Current.State == LeaseStateFree
}

func (e *LeasedIdentityPoolEntry) isAssignedTo(me string) bool {
	return e.Current.State == LeaseStateAssigned && e.Current.LeasedBy == me
}

func (e *LeasedIdentityPoolEntry) isBusy() bool {
	return e.Current.State == LeaseStateBusy
}

func (e *LeasedIdentityPoolEntry) assignTo(me string) error {
	if !e.isFree() {
		return errors.New("not free")
	}
	e.History = append(e.History, e.Current)
	e.Current.State = LeaseStateAssigned
	e.Current.LeasedBy = me
	e.Current.TransitionedAt = time.Now().UTC().Format(time.RFC3339)

	return nil
}

func (e *LeasedIdentityPoolEntry) use(me string) error {
	if !e.isAssignedTo(me) || e.isBusy() {
		return errors.New("not assigned to me or already busy")
	}
	e.History = append(e.History, e.Current)
	e.Current.State = LeaseStateBusy
	e.Current.TransitionedAt = time.Now().UTC().Format(time.RFC3339)

	return nil
}

func (e *LeasedIdentityPoolEntry) release(cleanup func() error) error {
	if e.Current.State == LeaseStateFree {
		return nil
	}
	e.History = append(e.History, e.Current)
	e.Current.State = LeaseStateFree
	e.Current.LeasedBy = ""
	e.Current.TransitionedAt = time.Now().UTC().Format(time.RFC3339)

	return cleanup()
}

type LeasedIdentityPoolState struct {
	file    *os.File
	entries []LeasedIdentityPoolEntry
}

func newLeasedIdentityPoolState(path string) (*LeasedIdentityPoolState, error) {

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return &LeasedIdentityPoolState{}, fmt.Errorf("failed to open managed identities pool state file %s: %w", path, err)
	}

	state := LeasedIdentityPoolState{file: f}

	if err := state.lock(); err != nil {
		return &LeasedIdentityPoolState{}, fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			log.Logger.WithError(err).Warn("failed to release managed identities pool state file lock")
		}
	}()

	if err := state.readUnlocked(); err != nil {
		return &LeasedIdentityPoolState{}, fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	if state.isInitialized() {
		return &state, nil
	}

	leasedRGs := strings.Fields(strings.TrimSpace(os.Getenv(LeasedMSIContainersEnvvar)))
	if len(leasedRGs) == 0 {
		return &LeasedIdentityPoolState{}, fmt.Errorf("expected envvar %s to not be empty", LeasedMSIContainersEnvvar)
	}

	if err := state.initializeUnlocked(leasedRGs); err != nil {
		return &LeasedIdentityPoolState{}, fmt.Errorf("failed to initialize managed identities pool state: %w", err)
	}

	return &state, nil
}

// func (state *LeasedIdentityPoolState) close() {
// 	err := state.file.Close()
// 	if err != nil {
// 		log.Logger.WithError(err).Warn("failed to close managed identities pool state file")
// 	}
// 	state.file = nil
// }

func (state *LeasedIdentityPoolState) useNextAssigned(me string) (string, error) {
	if err := state.lock(); err != nil {
		return "", fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			log.Logger.WithError(err).Warn("failed to release managed identities pool state file lock")
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
func (state *LeasedIdentityPoolState) assignNTo(me string, n uint8) error {
	if err := state.lock(); err != nil {
		return fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			log.Logger.WithError(err).Warn("failed to release managed identities pool state file lock")
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

func (state *LeasedIdentityPoolState) releaseByContainerName(resourceGroup string, cleanupFn func() error) error {
	if err := state.lock(); err != nil {
		return fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			log.Logger.WithError(err).Warn("failed to release managed identities pool state file lock")
		}
	}()

	err := state.readUnlocked()
	if err != nil {
		return fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}
	for i := range state.entries {
		if state.entries[i].ResourceGroup == resourceGroup {
			if err := state.entries[i].release(cleanupFn); err != nil {
				// cleanup is best effort, just log errors and continue
				log.Logger.WithError(err).Warnf("failed to release managed identities resource group %s", resourceGroup)
			}
			if err := state.writeUnlocked(); err != nil {
				return fmt.Errorf("failed to write managed identities pool state file: %w", err)
			}
			break
		}
	}

	return nil
}

func (state *LeasedIdentityPoolState) getLeasedIdentityContainers(me string) ([]string, error) {
	if err := state.lock(); err != nil {
		return nil, fmt.Errorf("failed to acquire managed identities pool state file lock: %w", err)
	}
	defer func() {
		if err := state.unlock(); err != nil {
			log.Logger.WithError(err).Warn("failed to release managed identities pool state file lock")
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

func (state *LeasedIdentityPoolState) lock() error {
	if err := syscall.Flock(int(state.file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return nil
}

func (state *LeasedIdentityPoolState) unlock() error {
	if err := syscall.Flock(int(state.file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("failed to release managed identities pool state file lock: %w", err)
	}
	return nil
}

func (state *LeasedIdentityPoolState) readUnlocked() error {

	if _, err := state.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of managed identities pool state file: %w", err)
	}
	data, err := io.ReadAll(state.file)
	if err != nil {
		return fmt.Errorf("failed to read managed identities pool state file: %w", err)
	}

	var entries []LeasedIdentityPoolEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to unmarshal managed identities pool state file: %w", err)
	}
	state.entries = entries

	return nil
}

func (state *LeasedIdentityPoolState) writeUnlocked() error {
	updated, err := yaml.Marshal(state.entries)
	if err != nil {
		return fmt.Errorf("failed to marshal updated managed identities pool state: %w", err)
	}

	// Rewrite the file in place under the existing flock
	if _, err := state.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to start of state file: %w", err)
	}
	if err := state.file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate state file: %w", err)
	}
	if _, err := state.file.Write(updated); err != nil {
		return fmt.Errorf("failed to write updated state file: %w", err)
	}
	if err := state.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync state file: %w", err)
	}

	return nil
}

func (state *LeasedIdentityPoolState) isInitialized() bool {
	return state.file != nil && state.entries != nil && len(state.entries) > 0
}

func (state *LeasedIdentityPoolState) initializeUnlocked(leasedRGs []string) error {
	entries := make([]LeasedIdentityPoolEntry, 0, len(leasedRGs))
	for _, rg := range leasedRGs {
		entries = append(entries, LeasedIdentityPoolEntry{
			ResourceGroup: rg,
			Current: LeaseEntry{
				State:          LeaseStateFree,
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
