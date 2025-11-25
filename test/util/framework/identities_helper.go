package framework

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"sigs.k8s.io/yaml"
)

const (
	LeasedMSIContainersEnvvar = "LEASED_MSI_CONTAINERS"
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

type managedIdentitiesOptions struct {
	*bicepDeploymentConfig
	usePooled bool
}

type ManagedIdentitiesOption func(*managedIdentitiesOptions)

type BicepDeploymentOrManagedIdentitiesOption interface{}

func (tc *perItOrDescribeTestContext) DeployManagedIdentities(
	ctx context.Context,
	bicepTemplateJSON []byte,
	opts ...BicepDeploymentOrManagedIdentitiesOption,
) (*armresources.DeploymentExtended, error) {

	cfg := &managedIdentitiesOptions{
		bicepDeploymentConfig: &bicepDeploymentConfig{
			scope:          BicepDeploymentScopeSubscription,
			deploymentName: "managed-identities",
			timeout:        45 * time.Minute,
			parameters:     map[string]interface{}{},
		},
		usePooled: UsePooledIdentities(),
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
		msiPool, err := GetLeasedIdentities()
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

	deploymentResult, err := tc.CreateBicepTemplateAndWait_v2(ctx,
		bicepTemplateJSON,
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
	ReleasedAt    string     `yaml:"releasedAt,omitempty"`
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

const msiPoolStateFileName = "identities-pool-state.yaml"

func msiPoolStateFilePath() string {
	return filepath.Join(sharedDir(), msiPoolStateFileName)
}

func CreateIdentitiesPoolStateFile() error {
	statePath := msiPoolStateFilePath()

	leasedRGs := strings.Fields(strings.TrimSpace(os.Getenv(LeasedMSIContainersEnvvar)))
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

func GetLeasedIdentities() (LeasedIdentityPool, error) {
	statePath := msiPoolStateFilePath()

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

func UsePooledIdentities() bool {
	pooled := strings.TrimSpace(os.Getenv("POOLED_IDENTITIES"))
	if pooled == "" {
		return false
	}
	b, _ := strconv.ParseBool(pooled)
	return b
}
