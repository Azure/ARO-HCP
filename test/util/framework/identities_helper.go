package framework

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"sigs.k8s.io/yaml"
)

const (
	UsePooledIdentitiesEnvvar = "POOLED_IDENTITIES"
	LeasedMSIContainersEnvvar = "LEASED_MSI_CONTAINERS"
)

const (
	msiPoolStateFileName = "identities-pool-state.yaml"
)

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

// MsiIdentities mirrors the MSI identity layout used in Bicep modules
// (non-msi-scoped-assignments.bicep / msi-scoped-assignments.bicep).
// We store the MSI *names* here so they can be passed directly to Bicep params.
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
	usePooled := tc.UsePooledIdentities()
	identities := LeasedIdentityPool{
		ResourceGroupName: resourceGroupName,
		Identities:        NewDefaultIdentities(),
	}

	if !usePooled {
		return identities, false, nil
	}

	leased, err := tc.GetLeasedIdentities()
	if err != nil {
		return LeasedIdentityPool{}, false, err
	}

	return leased, true, nil
}

type managedIdentitiesOptions struct {
	*bicepDeploymentConfig
	usePooled bool
}

type ManagedIdentitiesOption func(*managedIdentitiesOptions)

type BicepDeploymentOrManagedIdentitiesOption interface{}

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
	opts ...BicepDeploymentOrManagedIdentitiesOption,
) (*armresources.DeploymentExtended, error) {

	cfg := &managedIdentitiesOptions{
		bicepDeploymentConfig: &bicepDeploymentConfig{
			scope:          BicepDeploymentScopeSubscription,
			deploymentName: "managed-identities",
			timeout:        45 * time.Minute,
			parameters:     map[string]interface{}{},
		},
		usePooled: tc.UsePooledIdentities(),
	}

	for _, opt := range opts {
		switch o := opt.(type) {

		case BicepDeploymentOption:
			o(cfg.bicepDeploymentConfig)
		case ManagedIdentitiesOption:
			o(cfg)
		default:
			panic("unknown option type passed to DeployManagedIdentities()")
		}
	}

	var msiRGName string
	var identities Identities

	if cfg.usePooled {
		msiPool, err := tc.GetLeasedIdentities()
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
		"pooledIdentities":         identities,
		"nsgName":                  cfg.parameters["nsgName"],
		"vnetName":                 cfg.parameters["vnetName"],
		"subnetName":               cfg.parameters["subnetName"],
		"keyVaultName":             cfg.parameters["keyVaultName"],
	}
	if !cfg.usePooled {
		parameters["useMsiPool"] = false
	}

	deploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
		WithTemplateFromBytes(cfg.template),
		WithSubscriptionScope(),
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

func (tc *perItOrDescribeTestContext) CreateIdentitiesPoolStateFile() error {
	statePath := tc.msiPoolStateFilePath()

	leasedRGs := tc.perBinaryInvocationTestContext.LeasedIdentityContainers()
	if len(leasedRGs) == 0 {
		return fmt.Errorf("expected envvar %s to not be empty", LeasedMSIContainersEnvvar)
	}

	entries := make([]leasedIdentityPoolEntry, 0, len(leasedRGs))
	for _, rg := range leasedRGs {
		entries = append(entries, leasedIdentityPoolEntry{
			ResourceGroup: rg,
			State:         leaseStateFree,
		})
	}

	data, err := yaml.Marshal(entries)
	if err != nil {
		return fmt.Errorf("failed to marshal managed identities pool state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write managed identities pool state file %s: %w", statePath, err)
	}

	return nil
}

func (tc *perItOrDescribeTestContext) GetLeasedIdentities() (LeasedIdentityPool, error) {
	statePath := tc.msiPoolStateFilePath()

	f, err := os.OpenFile(statePath, os.O_RDWR, 0)
	if err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to open managed identities pool state file %s: %w", statePath, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := os.ReadFile(statePath)
	if err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to read managed identities pool state file %s: %w", statePath, err)
	}

	var entries []leasedIdentityPoolEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to unmarshal managed identities pool state file %s: %w", statePath, err)
	}

	var leasedRG string
	for i := range entries {
		if err := entries[i].Lease(); err != nil {
			continue
		}
		leasedRG = entries[i].ResourceGroup
		break
	}

	if leasedRG == "" {
		return LeasedIdentityPool{}, fmt.Errorf("all managed identities resource groups exhausted (state file: %s)", statePath)
	}

	updated, err := yaml.Marshal(entries)
	if err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to marshal updated managed identities pool state: %w", err)
	}

	tmpPath := statePath + ".tmp"
	if err := os.WriteFile(tmpPath, updated, 0644); err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to write updated managed identities pool state temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, statePath); err != nil {
		return LeasedIdentityPool{}, fmt.Errorf("failed to rename updated managed identities pool state file from %s to %s: %w", tmpPath, statePath, err)
	}

	return LeasedIdentityPool{
		ResourceGroupName: leasedRG,
		Identities:        NewDefaultIdentities(),
	}, nil
}

func (tc *perItOrDescribeTestContext) msiPoolStateFilePath() string {
	return filepath.Join(tc.perBinaryInvocationTestContext.sharedDir, msiPoolStateFileName)
}

type leaseState string

const (
	leaseStateFree leaseState = "free"
	leaseStateBusy leaseState = "busy"
)

type leasedIdentityPoolEntry struct {
	ResourceGroup string     `yaml:"resourceGroup"`
	State         leaseState `yaml:"state"`
	LeasedBy      string     `yaml:"leasedBy,omitempty"`
	LeasedAt      string     `yaml:"leasedAt,omitempty"`
	ReleasedAt    string     `yaml:"releasedAt,omitempty"` // not implemented
}

func (e *leasedIdentityPoolEntry) Lease() error {
	if e.State == leaseStateBusy {
		return fmt.Errorf("resource group %s is not free", e.ResourceGroup)
	}
	e.State = leaseStateBusy
	e.LeasedBy = fmt.Sprintf("pid:%d", os.Getpid())
	e.LeasedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

func (e *leasedIdentityPoolEntry) Release() error {
	if e.State == leaseStateFree {
		return nil
	}
	e.State = leaseStateFree
	e.ReleasedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}
