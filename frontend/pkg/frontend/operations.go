package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// AddAsyncOperationHeader adds an "Azure-AsyncOperation" header to the ResponseWriter
// with a URL of the operation status endpoint for the given OperationDocument.
func (f *Frontend) AddAsyncOperationHeader(writer http.ResponseWriter, request *http.Request, doc *database.OperationDocument) {
	// ARM will always add a Referer header, but
	// requests from test environments might not.
	referer := request.Referer()
	if referer == "" {
		f.logger.Info("Omitting " + arm.HeaderNameAsyncOperation + " header: no referer")
		return
	}

	u, err := url.ParseRequestURI(referer)
	if err != nil {
		f.logger.Error(err.Error())
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
	// ARM will always add a Referer header, but
	// requests from test environments might not.
	referer := request.Referer()
	if referer == "" {
		f.logger.Info("Omitting Location header: no referer")
		return
	}

	u, err := url.ParseRequestURI(referer)
	if err != nil {
		f.logger.Error(err.Error())
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
func (f *Frontend) ExposeOperation(writer http.ResponseWriter, request *http.Request, operationID string) error {
	ctx := request.Context()

	_, err := f.dbClient.UpdateOperationDoc(ctx, operationID, func(updateDoc *database.OperationDocument) bool {
		// There is no way to propagate a parse error here but it should
		// never fail since we are building a trusted resource ID string.
		operationID, err := arm.ParseResourceID(path.Join("/",
			"subscriptions", updateDoc.ExternalID.SubscriptionID,
			"providers", api.ProviderNamespace,
			"locations", f.location,
			api.OperationStatusResourceTypeName, operationID))
		if err != nil {
			f.logger.Error(err.Error())
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

// OperationIsVisible returns true if the request is being called from the same
// tenant and subscription that the operation originated in.
func (f *Frontend) OperationIsVisible(request *http.Request, doc *database.OperationDocument) bool {
	var visible = true

	tenantID := request.Header.Get(arm.HeaderNameHomeTenantID)
	clientID := request.Header.Get(arm.HeaderNameClientObjectID)
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	if doc.OperationID != nil {
		if doc.TenantID != "" && !strings.EqualFold(tenantID, doc.TenantID) {
			f.logger.Info(fmt.Sprintf("Unauthorized tenant '%s' in status request for operation '%s'", tenantID, doc.ID))
			visible = false
		}

		if doc.ClientID != "" && !strings.EqualFold(clientID, doc.ClientID) {
			f.logger.Info(fmt.Sprintf("Unauthorized client '%s' in status request for operation '%s'", clientID, doc.ID))
			visible = false
		}

		if !strings.EqualFold(subscriptionID, doc.OperationID.SubscriptionID) {
			f.logger.Info(fmt.Sprintf("Unauthorized subscription '%s' in status request for operation '%s'", subscriptionID, doc.ID))
			visible = false
		}
	} else {
		f.logger.Info(fmt.Sprintf("Status request for implicit operation '%s'", doc.ID))
		visible = false
	}

	return visible
}
