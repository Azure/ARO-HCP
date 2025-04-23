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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// AddAsyncOperationHeader adds an "Azure-AsyncOperation" header to the ResponseWriter
// with a URL of the operation status endpoint for the given OperationDocument.
func (f *Frontend) AddAsyncOperationHeader(writer http.ResponseWriter, request *http.Request, doc *database.OperationDocument) {
	logger := LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err.Error())
		return
	}

	u.Path = doc.OperationID.String()

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set(arm.HeaderNameAsyncOperation, u.String())
}

// AddLocationHeader adds a "Location" header to the ResponseWriter with a URL of the
// operation result endpoint for the given OperationDocument.
func (f *Frontend) AddLocationHeader(writer http.ResponseWriter, request *http.Request, doc *database.OperationDocument) {
	logger := LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err.Error())
		return
	}

	u.Path = path.Join("/",
		"subscriptions", doc.OperationID.SubscriptionID,
		"providers", api.ProviderNamespace,
		"locations", doc.OperationID.Location,
		api.OperationResultResourceTypeName, doc.OperationID.Name)

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
func (f *Frontend) ExposeOperation(writer http.ResponseWriter, request *http.Request, pk azcosmos.PartitionKey, operationID string) error {
	ctx := request.Context()

	_, err := f.dbClient.UpdateOperationDoc(ctx, pk, operationID, func(updateDoc *database.OperationDocument) bool {
		// There is no way to propagate a parse error here but it should
		// never fail since we are building a trusted resource ID string.
		operationID, err := azcorearm.ParseResourceID(path.Join("/",
			"subscriptions", updateDoc.ExternalID.SubscriptionID,
			"providers", api.ProviderNamespace,
			"locations", f.location,
			api.OperationStatusResourceTypeName, operationID))
		if err != nil {
			LoggerFromContext(ctx).Error(err.Error())
			return false
		}

		updateDoc.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
		updateDoc.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
		updateDoc.OperationID = operationID
		updateDoc.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)

		// If ARM passed a notification URI, acknowledge it.
		if updateDoc.NotificationURI != "" {
			writer.Header().Set(arm.HeaderNameAsyncNotification, "Enabled")
		}

		// Add callback header(s) based on the request method.
		switch request.Method {
		case http.MethodDelete, http.MethodPatch, http.MethodPost:
			f.AddLocationHeader(writer, request, updateDoc)
			fallthrough
		case http.MethodPut:
			f.AddAsyncOperationHeader(writer, request, updateDoc)
		}

		return true
	})
	if err != nil {
		// Delete any response headers that may have been added.
		writer.Header().Del(arm.HeaderNameAsyncNotification)
		writer.Header().Del(arm.HeaderNameAsyncOperation)
		writer.Header().Del("Location")
	}

	return err
}

// CancelOperation marks the status of an operation as canceled.
func (f *Frontend) CancelOperation(ctx context.Context, pk azcosmos.PartitionKey, operationID string) error {
	updated, err := f.dbClient.UpdateOperationDoc(ctx, pk, operationID, func(updateDoc *database.OperationDocument) bool {
		var cloudError = arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeCanceled,
			Message: "This operation was superseded by another",
		}
		return updateDoc.UpdateStatus(arm.ProvisioningStateCanceled, &cloudError)
	})
	// Disregard "not found" errors; a missing operation is effectively canceled.
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return err
	}
	if updated {
		logger := LoggerFromContext(ctx)
		logger.Info(fmt.Sprintf("Canceled operation '%s'", operationID))
	}
	return nil
}

// CancelActiveOperation marks the status of any active operation on the resource as canceled.
func (f *Frontend) CancelActiveOperation(ctx context.Context, resourceDoc *database.ResourceDocument) error {
	if resourceDoc.ActiveOperationID == "" {
		return nil
	}

	pk := database.NewPartitionKey(resourceDoc.ResourceID.SubscriptionID)
	return f.CancelOperation(ctx, pk, resourceDoc.ActiveOperationID)
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
