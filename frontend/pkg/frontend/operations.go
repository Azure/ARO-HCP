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
	"github.com/Azure/ARO-HCP/internal/ocm"
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

func (f *Frontend) StartOperation(writer http.ResponseWriter, request *http.Request, operationRequest database.OperationRequest, internalID ocm.InternalID) error {
	ctx := request.Context()

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return err
	}

	doc := database.NewOperationDocument(operationRequest)

	operationID, err := arm.ParseResourceID(path.Join("/",
		"subscriptions", resourceID.SubscriptionID,
		"providers", api.ProviderNamespace,
		"locations", f.location,
		api.OperationStatusResourceTypeName, doc.ID))
	if err != nil {
		return err
	}

	doc.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	doc.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	doc.ExternalID = resourceID
	doc.InternalID = internalID
	doc.OperationID = operationID
	doc.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)

	err = f.dbClient.CreateOperationDoc(ctx, doc)
	if err != nil {
		return err
	}

	// If ARM passed a notification URI, acknowledge it.
	if doc.NotificationURI != "" {
		writer.Header().Set(arm.HeaderNameAsyncNotification, "Enabled")
	}

	// Add callback header(s) based on the request method.
	switch request.Method {
	case http.MethodDelete, http.MethodPatch:
		f.AddLocationHeader(writer, request, doc)
		fallthrough
	case http.MethodPut:
		f.AddAsyncOperationHeader(writer, request, doc)
	}

	return nil
}

// OperationIsVisible returns true if the request is being called from the same
// tenant and subscription that the operation originated in.
func (f *Frontend) OperationIsVisible(request *http.Request, doc *database.OperationDocument) bool {
	var visible = true

	tenantID := request.Header.Get(arm.HeaderNameHomeTenantID)
	clientID := request.Header.Get(arm.HeaderNameClientObjectID)
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

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

	return visible
}
