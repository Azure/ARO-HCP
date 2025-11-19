package framework

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

const (
	LeasedMSIContainersEnvvar = "LEASED_MSI_CONTAINERS"
	MinMSICount               = 13
	MinLeasedRGCount          = 15
	LockFileName              = "aro-hcp-msi-pool-counter.lock"
)

type MSIPool struct {
	subscriptionID string
	msiClient      *armmsi.UserAssignedIdentitiesClient
	leasedRGs      []string
}

func NewMSIPool(ctx context.Context, subscriptionID string, cred azcore.TokenCredential) (*MSIPool, error) {
	msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create MSI client: %w", err)
	}

	leasedRGs := strings.Split(os.Getenv(LeasedMSIContainersEnvvar), " ")

	if len(leasedRGs) == 0 {
		return nil, fmt.Errorf("expected at least %d resource groups with precreated MSIs in envvar %s", MinLeasedRGCount, LeasedMSIContainersEnvvar)
	}

	return &MSIPool{
		subscriptionID: subscriptionID,
		msiClient:      msiClient,
		leasedRGs:      leasedRGs,
	}, nil
}

// GetLeasedMSIs acquires the next available RG using an atomic file-based counter
// and returns the MSIs from that resource group.
func (p *MSIPool) GetLeasedMSIs(ctx context.Context) ([]string, error) {
	rgIndex, err := p.acquireNextRGIndex()
	if err != nil {
		return nil, err
	}

	var msis []string
	pager := p.msiClient.NewListByResourceGroupPager(p.leasedRGs[rgIndex], nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list MSIs in %s: %w", p.leasedRGs[rgIndex], err)
		}

		for _, msi := range page.Value {
			msis = append(msis, *msi.ID)
		}
	}

	if len(msis) < MinMSICount {
		return nil, fmt.Errorf("not enough MSIs found in leased resource group %s, expected %d, got %d", p.leasedRGs[rgIndex], MinMSICount, len(msis))
	}

	return msis, nil
}

func (p *MSIPool) acquireNextRGIndex() (int, error) {
	lockFile := filepath.Join(os.TempDir(), LockFileName)

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

	if counter >= len(p.leasedRGs) {
		return 0, fmt.Errorf("all %d MSI resource groups exhausted (lock file: %s)", len(p.leasedRGs), lockFile)
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
