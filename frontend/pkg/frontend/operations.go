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

package frontend

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// AddAsyncOperationHeader adds an "Azure-AsyncOperation" header to the ResponseWriter
// with a URL of the operation status endpoint.
func (f *Frontend) AddAsyncOperationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err.Error())
		return
	}

	u.Path = operationID.String()

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set(arm.HeaderNameAsyncOperation, u.String())
}

// AddLocationHeader adds a "Location" header to the ResponseWriter with a URL of the
// operation result endpoint.
func (f *Frontend) AddLocationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err.Error())
		return
	}

	u.Path = path.Join("/",
		"subscriptions", operationID.SubscriptionID,
		"providers", operationID.ResourceType.Namespace,
		"locations", operationID.Location,
		api.OperationResultResourceTypeName, operationID.Name)

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set("Location", u.String())
}

// ExposeOperation fully initiates a new asynchronous operation by enriching
// the operation database item and adding the necessary response headers.
func (f *Frontend) ExposeOperation(writer http.ResponseWriter, request *http.Request, operationID string, transaction database.DBTransaction) {
	var patchOperations database.OperationDocumentPatchOperations

	// This should never fail since we are building a trusted resource ID string.
	operationResourceID, err := azcorearm.ParseResourceID(path.Join("/",
		"subscriptions", request.PathValue(PathSegmentSubscriptionID),
		"providers", api.ProviderNamespace,
		"locations", f.location,
		api.OperationStatusResourceTypeName, operationID))
	if err != nil {
		LoggerFromContext(request.Context()).Error(err.Error())
		return
	}

	patchOperations.SetTenantID(request.Header.Get(arm.HeaderNameHomeTenantID))
	patchOperations.SetClientID(request.Header.Get(arm.HeaderNameClientObjectID))
	patchOperations.SetOperationID(operationResourceID)

	notificationURI := request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	if notificationURI != "" {
		patchOperations.SetNotificationURI(&notificationURI)
	}

	transaction.PatchOperationDoc(operationID, patchOperations, nil)

	transaction.OnSuccess(func(result database.DBTransactionResult) {
		// If ARM passed a notification URI, acknowledge it.
		if notificationURI != "" {
			writer.Header().Set(arm.HeaderNameAsyncNotification, "Enabled")
		}

		// Add callback header(s) based on the request method.
		switch request.Method {
		case http.MethodDelete, http.MethodPatch, http.MethodPost:
			f.AddLocationHeader(writer, request, operationResourceID)
			fallthrough
		case http.MethodPut:
			f.AddAsyncOperationHeader(writer, request, operationResourceID)
		}
	})
}

// CancelActiveOperations queries for operation documents with a non-terminal
// status using the filters specified in opts. For every document returned in
// the query result, CancelActiveOperations adds patch operations to the given
// DBTransaction to mark the document as canceled.
func (f *Frontend) CancelActiveOperations(ctx context.Context, transaction database.DBTransaction, opts *database.DBClientListActiveOperationDocsOptions) error {
	var now = time.Now().UTC()

	iterator := f.dbClient.ListActiveOperationDocs(transaction.GetPartitionKey(), opts)

	for operationID, _ := range iterator.Items(ctx) {
		var patchOperations database.OperationDocumentPatchOperations

		patchOperations.SetLastTransitionTime(now)
		patchOperations.SetStatus(arm.ProvisioningStateCanceled)
		patchOperations.SetError(&arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeCanceled,
			Message: "This operation was superseded by another",
		})

		transaction.PatchOperationDoc(operationID, patchOperations, nil)
	}

	return iterator.GetError()
}

// OperationIsVisible returns true if the request is being called from the same
// tenant and subscription that the operation originated in.
func (f *Frontend) OperationIsVisible(request *http.Request, operationID string, doc *database.OperationDocument) bool {
	var visible = true

	logger := LoggerFromContext(request.Context())

	tenantID := request.Header.Get(arm.HeaderNameHomeTenantID)
	clientID := request.Header.Get(arm.HeaderNameClientObjectID)
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	if doc.OperationID != nil {
		if doc.TenantID != "" && !strings.EqualFold(tenantID, doc.TenantID) {
			logger.Info(fmt.Sprintf("Unauthorized tenant '%s' in status request for operation '%s'", tenantID, operationID))
			visible = false
		}

		if doc.ClientID != "" && !strings.EqualFold(clientID, doc.ClientID) {
			logger.Info(fmt.Sprintf("Unauthorized client '%s' in status request for operation '%s'", clientID, operationID))
			visible = false
		}

		if !strings.EqualFold(subscriptionID, doc.OperationID.SubscriptionID) {
			logger.Info(fmt.Sprintf("Unauthorized subscription '%s' in status request for operation '%s'", subscriptionID, operationID))
			visible = false
		}
	} else {
		logger.Info(fmt.Sprintf("Status request for implicit operation '%s'", operationID))
		visible = false
	}

	return visible
}
