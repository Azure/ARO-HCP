package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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

	// ARM will always add a Referer header, but
	// requests from test environments might not.
	referer := request.Referer()
	if referer == "" {
		logger.Info("Omitting " + arm.HeaderNameAsyncOperation + " header: no referer")
		return
	}

	u, err := url.ParseRequestURI(referer)
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

	// ARM will always add a Referer header, but
	// requests from test environments might not.
	referer := request.Referer()
	if referer == "" {
		logger.Info("Omitting Location header: no referer")
		return
	}

	u, err := url.ParseRequestURI(referer)
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
		case http.MethodDelete, http.MethodPatch:
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

// CancelActiveOperation marks the status of any active operation on the resource as canceled.
func (f *Frontend) CancelActiveOperation(ctx context.Context, resourceDoc *database.ResourceDocument) error {
	if resourceDoc.ActiveOperationID != "" {
		pk := database.NewPartitionKey(resourceDoc.ResourceID.SubscriptionID)
		updated, err := f.dbClient.UpdateOperationDoc(ctx, pk, resourceDoc.ActiveOperationID, func(updateDoc *database.OperationDocument) bool {
			return updateDoc.UpdateStatus(arm.ProvisioningStateCanceled, nil)
		})
		// Disregard "not found" errors; a missing operation is effectively canceled.
		if err != nil && !errors.Is(err, database.ErrNotFound) {
			return err
		}
		if updated {
			logger := LoggerFromContext(ctx)
			logger.Info(fmt.Sprintf("Canceled operation '%s'", resourceDoc.ActiveOperationID))
		}
	}
	return nil
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
