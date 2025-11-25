package framework

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const (
	LeasedMSIContainersEnvvar = "LEASED_MSI_CONTAINERS"
	MinLeasedRGCount          = 15
	LockFileName              = "aro-hcp-msi-pool-counter.lock"
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
type MsiPool struct {
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

// GetLeasedMSIs acquires the next available RG using an atomic file-based counter
// and returns the MSIs from that resource group, mapped by their logical roles.
func GetLeasedMSIs(ctx context.Context) (MsiPool, error) {

	leasedRGs := strings.Split(os.Getenv(LeasedMSIContainersEnvvar), " ")
	if len(leasedRGs) == 0 {
		return MsiPool{}, fmt.Errorf("expected at least %d resource groups with precreated MSIs in envvar %s", MinLeasedRGCount, LeasedMSIContainersEnvvar)
	}

	rgIndex, err := acquireNextRGIndex(leasedRGs)
	if err != nil {
		return MsiPool{}, err
	}

	return MsiPool{
		ResourceGroupName: leasedRGs[rgIndex],
		Identities:        NewDefaultIdentities(),
	}, nil
}

func acquireNextRGIndex(leasedRGs []string) (int, error) {
	lockFile := filepath.Join(sharedDir(), LockFileName)

	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to open lock file %s: %w", lockFile, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return 0, fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := io.ReadAll(f)
	if err != nil {
		return 0, fmt.Errorf("failed to read counter: %w", err)
	}

	counter := 0
	if len(data) > 0 {
		if _, err := fmt.Sscanf(string(data), "%d", &counter); err != nil {
			return 0, fmt.Errorf("failed to parse counter: %w", err)
		}
	}

	if counter >= len(leasedRGs) {
		return 0, fmt.Errorf("all %d MSI resource groups exhausted (lock file: %s)", len(leasedRGs), lockFile)
	}

	nextCounter := counter + 1
	if _, err := f.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%02d", nextCounter); err != nil {
		return 0, fmt.Errorf("failed to write counter: %w", err)
	}

	return counter, nil
}

func UsePooledIdentities() bool {
	pooled := strings.TrimSpace(os.Getenv("POOLED_IDENTITIES"))
	if pooled == "" {
		return false
	}
	b, _ := strconv.ParseBool(pooled)
	return b
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
		msiPool, err := GetLeasedMSIs(ctx)
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
