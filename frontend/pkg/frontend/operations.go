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
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AddAsyncOperationHeader adds an "Azure-AsyncOperation" header to the ResponseWriter
// with a URL of the operation status endpoint.
func AddAsyncOperationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := utils.LoggerFromContext(request.Context())

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
func AddLocationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := utils.LoggerFromContext(request.Context())

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

// CancelActiveOperations queries for operation documents with a non-terminal
// status using the filters specified in opts. For every document returned in
// the query result, CancelActiveOperations adds patch operations to the given
// DBTransaction to mark the document as canceled.
func (f *Frontend) CancelActiveOperations(ctx context.Context, transaction database.DBTransaction, opts *database.DBClientListActiveOperationDocsOptions) error {
	var now = time.Now().UTC()

	errs := []error{}
	subscriptionID := transaction.GetPartitionKey()
	iterator := f.dbClient.Operations(subscriptionID).ListActiveOperations(opts)
	for _, operation := range iterator.Items(ctx) {
		// TODO deep copy once available
		operationToWrite := *operation
		operationToWrite.LastTransitionTime = now
		operationToWrite.Status = arm.ProvisioningStateCanceled
		operationToWrite.Error = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeCanceled,
			Message: "This operation was superseded by another",
		}

		_, err := f.dbClient.Operations(subscriptionID).AddReplaceToTransaction(ctx, transaction, &operationToWrite, nil)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
		}
	}
	if err := iterator.GetError(); err != nil {
		errs = append(errs, utils.TrackError(err))
	}

	return errors.Join(errs...)
}

// OperationIsVisible returns true if the request is being called from the same
// tenant and subscription that the operation originated in.
func (f *Frontend) OperationIsVisible(request *http.Request, operation *api.Operation) bool {
	var visible = true

	logger := utils.LoggerFromContext(request.Context())

	tenantID := request.Header.Get(arm.HeaderNameHomeTenantID)
	clientID := request.Header.Get(arm.HeaderNameClientObjectID)
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	if operation.OperationID != nil {
		if operation.TenantID != "" && !strings.EqualFold(tenantID, operation.TenantID) {
			logger.Info("Unauthorized tenant in status request", "tenantID", tenantID, "operationID", operation.OperationID)
			visible = false
		}

		if operation.ClientID != "" && !strings.EqualFold(clientID, operation.ClientID) {
			logger.Info("Unauthorized client in status request", "clientID", clientID, "operationID", operation.OperationID)
			visible = false
		}

		if !strings.EqualFold(subscriptionID, operation.OperationID.SubscriptionID) {
			logger.Info("Unauthorized subscription in status request", "subscriptionID", subscriptionID, "operationID", operation.OperationID)
			visible = false
		}
	} else {
		logger.Info("Status request for implicit operation", "operationID", operation.OperationID)
		visible = false
	}

	return visible
}
