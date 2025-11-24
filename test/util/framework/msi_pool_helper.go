package framework

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
		Identities: Identities{
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
		}}, nil
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
