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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// Helper to run command on VM
func RunVMCommand(ctx context.Context, tc interface {
	SubscriptionID(ctx context.Context) (string, error)
	AzureCredential() (azcore.TokenCredential, error)
}, resourceGroup, vmName, command string, pollTimeout time.Duration) (string, error) {
	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return "", err
	}

	azCreds, err := tc.AzureCredential()
	if err != nil {
		return "", err
	}

	computeClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, azCreds, nil)
	if err != nil {
		return "", err
	}

	runCommandInput := armcompute.RunCommandInput{
		CommandID: to.Ptr("RunShellScript"),
		Script: []*string{
			to.Ptr(command),
		},
	}

	poller, err := computeClient.BeginRunCommand(ctx, resourceGroup, vmName, runCommandInput, nil)
	if err != nil {
		return "", err
	}

	// Create a timeout context to avoid waiting too long on VM command failures
	// VM commands should complete quickly (within a few minutes at most)
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	result, err := poller.PollUntilDone(pollCtx, nil)
	if err != nil {
		return "", err
	}

	if len(result.Value) > 0 && result.Value[0].Message != nil {
		// Azure Run Command returns output in format:
		// "Enable succeeded: \n[stdout]\n<actual output>\n[stderr]\n<errors>"
		// We need to extract stdout and stderr content
		message := *result.Value[0].Message

		// Find the stdout section
		stdoutStart := strings.Index(message, "[stdout]\n")
		if stdoutStart == -1 {
			// If no stdout marker, return the whole message
			return message, nil
		}

		// Skip past the "[stdout]\n" marker
		stdoutStart += len("[stdout]\n")

		// Find where stderr starts (if present)
		stderrStart := strings.Index(message[stdoutStart:], "\n[stderr]")

		var output string
		if stderrStart == -1 {
			// No stderr marker, take everything after stdout
			output = message[stdoutStart:]
		} else {
			// Take only the stdout section
			output = message[stdoutStart : stdoutStart+stderrStart]

			// Extract and inspect stderr
			stderrAbsoluteStart := stdoutStart + stderrStart + len("\n[stderr]\n")
			if stderrAbsoluteStart < len(message) {
				stderr := strings.TrimSpace(message[stderrAbsoluteStart:])
				if stderr != "" {
					// Return an error if stderr is not empty
					return "", fmt.Errorf("%s", stderr)
				}
			}
		}

		return strings.TrimSpace(output), nil
	}

	return "", nil
}

// GetVirtualMachinesInResourceGroup lists all VMs in the given resource group
// if the VM list contains at least expectedMinimumCount items
func GetVirtualMachinesInResourceGroup(
	ctx context.Context,
	computeClientFactory *armcompute.ClientFactory,
	resourceGroupName string,
	expectedMinimumCount int,
) ([]*armcompute.VirtualMachine, error) {
	vmClient := computeClientFactory.NewVirtualMachinesClient()
	var vms []*armcompute.VirtualMachine
	pager := vmClient.NewListPager(resourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VMs in resource group %q: %w", resourceGroupName, err)
		}
		vms = append(vms, page.Value...)
	}

	if len(vms) < expectedMinimumCount {
		vmNames := make([]string, 0, len(vms))
		for _, vm := range vms {
			if vm.Name != nil {
				vmNames = append(vmNames, *vm.Name)
			}
		}
		return nil, fmt.Errorf(
			"not enough VMs in resource group %q: expecting %d, got %d (%v)",
			resourceGroupName,
			expectedMinimumCount,
			len(vms),
			vmNames,
		)
	}

	return vms, nil
}

// GetVirtualMachineConsoleLog retrieves the boot diagnostics serial console log from an Azure VM.
// Returns an io.ReadCloser for streaming the console log data. The caller is responsible for closing the reader.
// Returns an error if the retrieval fails or boot diagnostics is not enabled.
func GetVirtualMachineConsoleLog(
	ctx context.Context,
	computeClientFactory *armcompute.ClientFactory,
	resourceGroupName string,
	vmName string,
) (io.ReadCloser, error) {
	vmClient := computeClientFactory.NewVirtualMachinesClient()

	// Retrieve boot diagnostics data which includes the serial console log
	result, err := vmClient.RetrieveBootDiagnosticsData(ctx, resourceGroupName, vmName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve boot diagnostics data for VM %q: %w", vmName, err)
	}

	// Check if serial console log URI is available
	if result.SerialConsoleLogBlobURI == nil || *result.SerialConsoleLogBlobURI == "" {
		return nil, fmt.Errorf("serial console log URI not available for VM %q (boot diagnostics may not be enabled)", vmName)
	}

	// Fetch the actual log content from the blob storage URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *result.SerialConsoleLogBlobURI, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for console log blob for VM %q: %w", vmName, err)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch console log from blob storage for VM %q: %w", vmName, err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to fetch console log for VM %q, HTTP status: %d", vmName, resp.StatusCode)
	}

	return resp.Body, nil
}

// DownloadAllVirtualMachineConsoleLogs downloads boot diagnostics console logs for all VMs
// in the specified resource group and saves them to the target directory.
// Each log file is named "<vmName>-console.log". VMs without boot diagnostics enabled are skipped.
// Returns an error if the directory cannot be created or if listing VMs fails.
func DownloadAllVirtualMachineConsoleLogs(
	ctx context.Context,
	computeClientFactory *armcompute.ClientFactory,
	resourceGroupName string,
	targetDirectory string,
) error {
	logger := ginkgo.GinkgoLogr

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %q: %w", targetDirectory, err)
	}

	// List all VMs in the resource group
	// Use 0 as expectedMinimumCount to return immediately with whatever VMs exist,
	// as we expect to run this after a node pool deployment failures
	vms, err := GetVirtualMachinesInResourceGroup(ctx, computeClientFactory, resourceGroupName, 0)
	if err != nil {
		return fmt.Errorf("failed to list VMs: %w", err)
	}

	if len(vms) == 0 {
		logger.Info("no VMs found in resource group", "resourceGroup", resourceGroupName)
		return nil
	}

	// Download console log for each VM
	var downloadErrors []string
	for _, vm := range vms {
		if vm.Name == nil {
			continue
		}

		logReader, err := GetVirtualMachineConsoleLog(ctx, computeClientFactory, resourceGroupName, *vm.Name)
		if err != nil {
			// Don't fail completely if one VM doesn't have boot diagnostics enabled
			logger.Info("failed to fetch VM console log", "vmName", *vm.Name)
			downloadErrors = append(downloadErrors, fmt.Sprintf("VM %q: %v", *vm.Name, err))
			continue
		}

		// Save the console log to a file
		logFilePath := filepath.Join(targetDirectory, fmt.Sprintf("%s-console.log", *vm.Name))
		logFile, err := os.Create(logFilePath)
		if err != nil {
			logReader.Close()
			downloadErrors = append(downloadErrors, fmt.Sprintf("VM %q: failed to create file: %v", *vm.Name, err))
			continue
		}

		_, copyErr := io.Copy(logFile, logReader)
		closeErr := logFile.Close()
		logReader.Close()

		err = errors.Join(copyErr, closeErr)
		if err != nil {
			downloadErrors = append(downloadErrors, fmt.Sprintf("VM %q: failed to write log: %v", *vm.Name, err))
			continue
		} else {
			logger.Info("VM console log fetched", "vmName", *vm.Name, "targetPath", logFilePath)
		}
	}

	// If there were any errors, return them as a combined error message
	if len(downloadErrors) > 0 {
		return fmt.Errorf("encountered errors downloading console logs:\n  - %s", strings.Join(downloadErrors, "\n  - "))
	}

	return nil
}
