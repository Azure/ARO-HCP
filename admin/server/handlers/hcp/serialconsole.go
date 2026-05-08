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

package hcp

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

const (
	maxBytes = 100 * 1024 * 1024 // 100MB
)

// HCPSerialConsoleHandler handles requests to retrieve VM serial console logs
// for debugging purposes. This endpoint allows SREs to access boot diagnostics
// and console output from VMs in the HCP cluster's managed resource group.
type HCPSerialConsoleHandler struct {
	resourcesDBClient      database.ResourcesDBClient
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
}

// NewHCPSerialConsoleHandler creates a new serial console handler with the required dependencies
func NewHCPSerialConsoleHandler(
	resourcesDBClient database.ResourcesDBClient,
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
) *HCPSerialConsoleHandler {
	return &HCPSerialConsoleHandler{
		resourcesDBClient:      resourcesDBClient,
		fpaCredentialRetriever: fpaCredentialRetriever,
	}
}

// ServeHTTP handles GET requests to retrieve serial console output for a specified VM.
func (h *HCPSerialConsoleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"invalid resource identifier in request",
		)
	}

	vmName := request.URL.Query().Get("vmName")
	if vmName == "" {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"vmName query parameter is required",
		)
	}

	if !validation.IsValidAzureVMName(vmName) {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"vmName contains invalid characters or format",
		)
	}

	hcp, err := h.resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCP from database: %w", err))
	}

	subscription, err := h.resourcesDBClient.Subscriptions().Get(request.Context(), resourceID.SubscriptionID)
	if database.IsNotFoundError(err) {
		return arm.NewCloudError(
			http.StatusNotFound,
			arm.CloudErrorCodeNotFound,
			"",
			"subscription %s not found", resourceID.SubscriptionID,
		)
	}
	if err != nil {
		return utils.TrackError(err)
	}

	tokenCredential, err := h.fpaCredentialRetriever.RetrieveCredential(*subscription.Properties.TenantId)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to retrieve Azure credentials: %w", err))
	}

	computeClient, err := armcompute.NewVirtualMachinesClient(hcp.ID.SubscriptionID, tokenCredential, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Azure compute client: %w", err))
	}

	managedResourceGroup := hcp.CustomerProperties.Platform.ManagedResourceGroup
	sasTokenExpirationMinutes := int32(5)
	options := &armcompute.VirtualMachinesClientRetrieveBootDiagnosticsDataOptions{
		SasURIExpirationTimeInMinutes: &sasTokenExpirationMinutes,
	}
	result, err := computeClient.RetrieveBootDiagnosticsData(
		request.Context(),
		managedResourceGroup,
		vmName,
		options,
	)
	if err != nil {
		var azErr *azcore.ResponseError
		if ok := errors.As(err, &azErr); ok && azErr != nil {
			if azErr.StatusCode == http.StatusNotFound {
				return arm.NewCloudError(
					http.StatusNotFound,
					arm.CloudErrorCodeNotFound,
					"",
					"VM %s not found in resource group %s", vmName, managedResourceGroup,
				)
			}
			if azErr.StatusCode == http.StatusConflict {
				return arm.NewCloudError(
					http.StatusConflict,
					arm.CloudErrorCodeConflict,
					"",
					"Boot diagnostics are unexpectedly not enabled for VM %s. Serial console logs require boot diagnostics to be enabled.", vmName,
				)
			}
		}
		return utils.TrackError(fmt.Errorf("failed to retrieve boot diagnostics data for VM %s: %w", vmName, err))
	}

	if result.SerialConsoleLogBlobURI == nil || *result.SerialConsoleLogBlobURI == "" {
		return arm.NewCloudError(
			http.StatusNotFound,
			arm.CloudErrorCodeNotFound,
			"",
			"serial console not available for VM %s",
			vmName,
		)
	}

	blobReq, err := http.NewRequestWithContext(request.Context(), http.MethodGet, *result.SerialConsoleLogBlobURI, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create blob request: %w", err))
	}

	httpClient := &http.Client{}
	blobResp, err := httpClient.Do(blobReq)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to download serial console log: %w", err))
	}
	defer blobResp.Body.Close()

	if blobResp.StatusCode != http.StatusOK {
		return utils.TrackError(fmt.Errorf("failed to download serial console log: unexpected status %d", blobResp.StatusCode))
	}

	limitedReader := io.LimitReader(blobResp.Body, maxBytes)
	logData, err := io.ReadAll(limitedReader)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to read serial console log: %w", err))
	}

	// stream response as text/plain and prevent caching of potentially sensitive console output
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Expires", "0")

	logger := utils.LoggerFromContext(request.Context())

	if int64(len(logData)) == maxBytes {
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/*", maxBytes-1))
		writer.WriteHeader(http.StatusPartialContent)
		warning := "WARNING: Serial console log output limited to first 100MB. Full logs may be truncated.\n\n"
		_, err = writer.Write([]byte(warning))
		if err != nil {
			logger.Error(err, "failed to write serial console log truncation warning", "vmName", vmName)
			return utils.TrackError(fmt.Errorf("failed to write serial console log: %w", err))
		}
	}

	_, err = writer.Write(logData)
	if err != nil {
		logger.Error(err, "failed to write serial console log", "vmName", vmName)
		return utils.TrackError(fmt.Errorf("failed to write serial console log: %w", err))
	}

	return nil
}
