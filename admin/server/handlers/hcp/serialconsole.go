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
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// HCPSerialConsoleHandler handles requests to retrieve VM serial console logs
// for debugging purposes. This endpoint allows SREs to access boot diagnostics
// and console output from VMs in the HCP cluster's managed resource group.
type HCPSerialConsoleHandler struct {
	dbClient               database.DBClient
	csClient               ocm.ClusterServiceClientSpec
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
}

// NewHCPSerialConsoleHandler creates a new serial console handler with the required dependencies
func NewHCPSerialConsoleHandler(
	dbClient database.DBClient,
	csClient ocm.ClusterServiceClientSpec,
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
) *HCPSerialConsoleHandler {
	return &HCPSerialConsoleHandler{
		dbClient:               dbClient,
		csClient:               csClient,
		fpaCredentialRetriever: fpaCredentialRetriever,
	}
}

// ServeHTTP handles GET requests to retrieve serial console output for a specified VM.
// Query parameters:
//   - vmName (required): The name of the VM to retrieve console logs
func (h *HCPSerialConsoleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) error {
	// get the azure resource ID for this HCP
	resourceID, err := utils.ResourceIDFromContext(request.Context())
	if err != nil {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"invalid resource identifier in request",
		)
	}

	//Extract and validate vmName query parameter
	vmName := request.URL.Query().Get("vmName")
	if vmName == "" {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"vmName query parameter is required",
		)
	}

	// get HCP details
	hcp, err := h.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(request.Context(), resourceID.Name)
	if err != nil {
		return fmt.Errorf("failed to get HCP from database: %w", err)
	}

	// get CS cluster data to retrieve tenant ID
	csCluster, err := h.csClient.GetCluster(request.Context(), hcp.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return fmt.Errorf("failed to get CS cluster data: %w", err)
	}

	// get FPA credentials for customer tenant
	tokenCredential, err := h.fpaCredentialRetriever.RetrieveCredential(csCluster.Azure().TenantID())
	if err != nil {
		return fmt.Errorf("failed to get FPA token credentials: %w", err)
	}

	// Create Azure Compute client for customer subscription
	computeClient, err := armcompute.NewVirtualMachinesClient(hcp.ID.SubscriptionID, tokenCredential, nil)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}

	// Retrieve boot diagnostics data containing serial console blob URI
	managedResourceGroup := hcp.CustomerProperties.Platform.ManagedResourceGroup
	result, err := computeClient.RetrieveBootDiagnosticsData(
		request.Context(),
		managedResourceGroup,
		vmName,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to retrieve boot diagnostics data: %w", err)
	}

	// verify serial console log blob URI is available
	if result.SerialConsoleLogBlobURI == nil || *result.SerialConsoleLogBlobURI == "" {
		return arm.NewCloudError(
			http.StatusNotFound,
			arm.CloudErrorCodeNotFound,
			"",
			"serial console not available for VM %s",
			vmName,
		)
	}

	// fetch blob content via HTTP GET
	// The blob URI contains a SAS token for authentication, so we can use a simple HTTP GET
	blobReq, err := http.NewRequestWithContext(request.Context(), http.MethodGet, *result.SerialConsoleLogBlobURI, nil)
	if err != nil {
		return fmt.Errorf("failed to create blob request: %w", err)
	}

	// download blob content
	httpClient := &http.Client{}
	blobResp, err := httpClient.Do(blobReq)
	if err != nil {
		return fmt.Errorf("failed to download serial console log: %w", err)
	}
	defer blobResp.Body.Close()

	if blobResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download serial console log: unexpected status %d", blobResp.StatusCode)
	}

	// stream response as text/plain
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, err = io.Copy(writer, blobResp.Body)
	if err != nil {
		// If we fail during streaming, log it
		return fmt.Errorf("failed to stream serial console log: %w", err)
	}

	return nil
}
