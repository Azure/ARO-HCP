package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"net/url"
	"path"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"

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
	now := time.Now().UTC()
	ctx := request.Context()

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return err
	}

	azcoreOperationID, err := azcorearm.ParseResourceID(path.Join("/",
		"subscriptions", resourceID.SubscriptionID,
		"providers", api.ProviderNamespace,
		"locations", f.location,
		api.OperationStatusResourceTypeName, uuid.New().String()))
	if err != nil {
		return err
	}

	doc := &database.OperationDocument{
		ID:                 azcoreOperationID.Name,
		TenantID:           request.Header.Get(arm.HeaderNameHomeTenantID),
		ClientID:           request.Header.Get(arm.HeaderNameClientObjectID),
		Request:            operationRequest,
		ExternalID:         resourceID,
		InternalID:         internalID,
		OperationID:        &arm.ResourceID{ResourceID: *azcoreOperationID},
		NotificationURI:    request.Header.Get(arm.HeaderNameAsyncNotificationURI),
		StartTime:          now,
		LastTransitionTime: now,
		Status:             arm.ProvisioningStateAccepted,
	}

	err = f.dbClient.SetOperationDoc(ctx, doc)
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
