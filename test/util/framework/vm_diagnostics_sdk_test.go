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
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	computefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5/fake"
)

const failedVMName = "np-1-abcde-t7pf8"

var failedVMInstanceView = armcompute.VirtualMachineInstanceView{
	Statuses: []*armcompute.InstanceViewStatus{
		{
			Code:    to.Ptr("ProvisioningState/failed/InternalExecutionError"),
			Message: to.Ptr("An internal execution error occurred. Please retry later."),
		},
	},
}

// fakeComputeClientFactory serves one VM in provisioning state Failed. Boot
// diagnostics retrieval for it fails with bootDiagnosticsStatus, and its
// instance view fails with instanceViewStatus unless that is 0.
func fakeComputeClientFactory(t *testing.T, bootDiagnosticsStatus, instanceViewStatus int) *armcompute.ClientFactory {
	t.Helper()
	srv := computefake.ServerFactory{
		VirtualMachinesServer: computefake.VirtualMachinesServer{
			NewListPager: func(resourceGroupName string, options *armcompute.VirtualMachinesClientListOptions) (resp azfake.PagerResponder[armcompute.VirtualMachinesClientListResponse]) {
				resp.AddPage(http.StatusOK, armcompute.VirtualMachinesClientListResponse{VirtualMachineListResult: armcompute.VirtualMachineListResult{
					Value: []*armcompute.VirtualMachine{{
						Name:       to.Ptr(failedVMName),
						Location:   to.Ptr("uksouth"),
						Properties: &armcompute.VirtualMachineProperties{ProvisioningState: to.Ptr("Failed")},
					}},
				}}, nil)
				return
			},
			RetrieveBootDiagnosticsData: func(ctx context.Context, resourceGroupName, vmName string, options *armcompute.VirtualMachinesClientRetrieveBootDiagnosticsDataOptions) (resp azfake.Responder[armcompute.VirtualMachinesClientRetrieveBootDiagnosticsDataResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(bootDiagnosticsStatus, http.StatusText(bootDiagnosticsStatus))
				return
			},
			InstanceView: func(ctx context.Context, resourceGroupName, vmName string, options *armcompute.VirtualMachinesClientInstanceViewOptions) (resp azfake.Responder[armcompute.VirtualMachinesClientInstanceViewResponse], errResp azfake.ErrorResponder) {
				if instanceViewStatus != 0 {
					errResp.SetResponseError(instanceViewStatus, http.StatusText(instanceViewStatus))
					return
				}
				resp.SetResponse(http.StatusOK, armcompute.VirtualMachinesClientInstanceViewResponse{VirtualMachineInstanceView: failedVMInstanceView}, nil)
				return
			},
		},
	}
	factory, err := armcompute.NewClientFactory(fakeSubscriptionID, &azfake.TokenCredential{}, fakeClientOptions(computefake.NewServerFactoryTransport(&srv)))
	if err != nil {
		t.Fatalf("failed to create fake compute client factory: %v", err)
	}
	return factory
}

func TestDownloadAllVirtualMachineConsoleLogsSavesInstanceViewWhenBootDiagnosticsFail(t *testing.T) {
	targetDirectory := t.TempDir()
	factory := fakeComputeClientFactory(t, http.StatusBadRequest, 0)

	err := DownloadAllVirtualMachineConsoleLogs(context.Background(), factory, "rg--managed", targetDirectory)
	if err == nil {
		t.Fatalf("DownloadAllVirtualMachineConsoleLogs() error = nil, want the boot diagnostics failure")
	}
	for _, want := range []string{
		failedVMName,
		"failed to retrieve boot diagnostics data",
		`VM provisioningState="Failed"`,
		`ProvisioningState/failed/InternalExecutionError: "An internal execution error occurred. Please retry later."`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("DownloadAllVirtualMachineConsoleLogs() error = %q, want it to contain %q", err, want)
		}
	}

	saved, readErr := os.ReadFile(filepath.Join(targetDirectory, failedVMName+"-instance-view.json"))
	if readErr != nil {
		t.Fatalf("instance view artifact was not saved: %v", readErr)
	}
	var instanceView armcompute.VirtualMachineInstanceView
	if err := json.Unmarshal(saved, &instanceView); err != nil {
		t.Fatalf("instance view artifact is not valid JSON: %v", err)
	}
	if len(instanceView.Statuses) != 1 || *instanceView.Statuses[0].Code != *failedVMInstanceView.Statuses[0].Code {
		t.Errorf("instance view artifact statuses = %s, want the VM's instance view", saved)
	}
	if _, statErr := os.Stat(filepath.Join(targetDirectory, failedVMName+"-console.log")); !os.IsNotExist(statErr) {
		t.Errorf("console log file exists for a VM whose boot diagnostics failed (stat error: %v)", statErr)
	}
}

func TestDescribeVirtualMachineStateReportsItsOwnFailures(t *testing.T) {
	vm := &armcompute.VirtualMachine{
		Name:       to.Ptr(failedVMName),
		Properties: &armcompute.VirtualMachineProperties{ProvisioningState: to.Ptr("Failed")},
	}

	t.Run("instance view cannot be read", func(t *testing.T) {
		targetDirectory := t.TempDir()
		vmClient := fakeComputeClientFactory(t, http.StatusBadRequest, http.StatusInternalServerError).NewVirtualMachinesClient()

		got := describeVirtualMachineState(context.Background(), vmClient, "rg--managed", vm, targetDirectory)
		if !strings.Contains(got, `VM provisioningState="Failed", instance view unavailable:`) || !strings.Contains(got, "500") {
			t.Errorf("describeVirtualMachineState() = %q, want the provisioning state and the instance view error", got)
		}
		if _, err := os.Stat(filepath.Join(targetDirectory, failedVMName+"-instance-view.json")); !os.IsNotExist(err) {
			t.Errorf("instance view artifact exists although the instance view could not be read (stat error: %v)", err)
		}
	})

	t.Run("instance view cannot be saved", func(t *testing.T) {
		missingDirectory := filepath.Join(t.TempDir(), "does-not-exist")
		vmClient := fakeComputeClientFactory(t, http.StatusBadRequest, 0).NewVirtualMachinesClient()

		got := describeVirtualMachineState(context.Background(), vmClient, "rg--managed", vm, missingDirectory)
		if !strings.Contains(got, "ProvisioningState/failed/InternalExecutionError") || !strings.Contains(got, "failed to save instance view") {
			t.Errorf("describeVirtualMachineState() = %q, want the instance view statuses and the save error", got)
		}
	})
}
