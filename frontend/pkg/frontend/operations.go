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

	"k8s.io/apimachinery/pkg/util/wait"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AddAsyncOperationHeader adds an "Azure-AsyncOperation" header to the ResponseWriter
// with a URL of the operation status endpoint.
func AddAsyncOperationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := utils.LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err, "failed to parse request referer")
		return
	}

	u.Path = operationID.String()

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set(coreapi.HeaderNameAsyncOperation, u.String())
}

// AddLocationHeader adds a "Location" header to the ResponseWriter with a URL of the
// operation result endpoint.
func AddLocationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := utils.LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err, "failed to parse request referer")
		return
	}

	u.Path = path.Join("/",
		"subscriptions", operationID.SubscriptionID,
		"providers", operationID.ResourceType.Namespace,
		"locations", operationID.Location,
		coreapi.OperationResultResourceTypeName, operationID.Name)

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set("Location", u.String())
}

// operationReadLagBackoff bounds the retry budget for operation reads that
// return NotFound because the write has not yet replicated. The azcosmos SDK
// does not propagate session tokens between requests, so under the account's
// Session consistency a read can be served by a replica that has not yet
// applied a write committed milliseconds earlier. The observed lag is well
// under a second; five attempts spread over roughly 1.5 seconds cover it with
// margin while still failing fast on genuinely unknown operation IDs.
var operationReadLagBackoff = wait.Backoff{
	Duration: 100 * time.Millisecond,
	Factor:   2,
	Jitter:   0.1,
	Steps:    5,
}

// getOperation returns the Operation document with the given name, retrying
// briefly on NotFound. The operation URL returned with a 202 response must
// resolve on the first poll; without this retry a read served by a lagging
// replica fails that first poll with a 404 that ARM SDK pollers treat as fatal
// (see ARO-30240). Once the retry budget is exhausted the last NotFound error
// is returned unchanged, so genuinely unknown operation IDs still produce a
// 404 response. Errors other than NotFound are never retried.
func (f *Frontend) getOperation(ctx context.Context, subscriptionID, operationName string) (*coreapi.Operation, error) {
	logger := utils.LoggerFromContext(ctx)

	var operation *coreapi.Operation
	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, operationReadLagBackoff, func(ctx context.Context) (bool, error) {
		var getErr error
		operation, getErr = f.resourcesDBClient.Operations(subscriptionID).Get(ctx, operationName)
		if getErr == nil {
			return true, nil
		}
		lastErr = getErr
		if !cosmosstorageutils.IsNotFoundError(getErr) {
			return false, getErr
		}
		logger.Info("operation not found, retrying read to absorb replication lag",
			"operation_name", operationName)
		return false, nil
	})
	switch {
	case err == nil:
		return operation, nil
	case ctx.Err() != nil:
		// The request went away; surface that rather than the last read error.
		return nil, err
	case wait.Interrupted(err):
		// The retry budget ran out; the final NotFound is the real answer.
		return nil, lastErr
	default:
		return nil, err
	}
}

// OperationIsVisible returns true if the request is being called from the same
// tenant and subscription that the operation originated in.
func (f *Frontend) OperationIsVisible(request *http.Request, operation *coreapi.Operation) bool {
	var visible = true

	logger := utils.LoggerFromContext(request.Context())

	tenantID := request.Header.Get(coreapi.HeaderNameHomeTenantID)
	clientID := request.Header.Get(coreapi.HeaderNameClientObjectID)
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	if operation.OperationID != nil {
		if operation.TenantID != "" && !strings.EqualFold(tenantID, operation.TenantID) {
			logger.Info(fmt.Sprintf("Unauthorized tenant '%s' in status request for operation '%s'", tenantID, operation.OperationID))
			visible = false
		}

		if operation.ClientID != "" && !strings.EqualFold(clientID, operation.ClientID) {
			logger.Info(fmt.Sprintf("Unauthorized client '%s' in status request for operation '%s'", clientID, operation.OperationID))
			visible = false
		}

		if !strings.EqualFold(subscriptionID, operation.OperationID.SubscriptionID) {
			logger.Info(fmt.Sprintf("Unauthorized subscription '%s' in status request for operation '%s'", subscriptionID, operation.OperationID))
			visible = false
		}
	} else {
		logger.Info(fmt.Sprintf("Status request for implicit operation '%s'", operation.OperationID))
		visible = false
	}

	return visible
}
