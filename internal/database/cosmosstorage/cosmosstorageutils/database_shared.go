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

package cosmosstorageutils

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// ErrAmbiguousResult occurs when a database query intended
// to yield a single item unexpectedly yields multiple items.
var ErrAmbiguousResult = errors.New("ambiguous result")

func isResponseError(err error, statusCode int) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) && responseError.StatusCode == statusCode {
		return true
	}
	var stepError *TransactionStepError
	return errors.As(err, &stepError) && stepError.HTTPStatusCode == statusCode
}

// IsNotFoundError returns true if err represents an HTTP 404 Not Found response.
func IsNotFoundError(err error) bool {
	return isResponseError(err, http.StatusNotFound)
}

// IsConflictError returns true if err represents an HTTP 409 Conflict response.
func IsConflictError(err error) bool {
	return isResponseError(err, http.StatusConflict)
}

// IsPreconditionFailedError returns true if err represents an HTTP 412 Precondition Failed response.
func IsPreconditionFailedError(err error) bool {
	return isResponseError(err, http.StatusPreconditionFailed)
}

func IsBadRequestError(err error) bool {
	return isResponseError(err, http.StatusBadRequest)
}

// NewPartitionKey creates a partition key from an Azure subscription ID.
func NewPartitionKey(subscriptionID string) azcosmos.PartitionKey {
	return azcosmos.NewPartitionKeyString(strings.ToLower(subscriptionID))
}

type DBClientIteratorItem[T any] iter.Seq2[string, *T]

type DBClientIterator[T any] interface {
	Items(ctx context.Context) DBClientIteratorItem[T]
	GetContinuationToken() string
	GetError() error
}

// DBClientListResourceDocsOptions allows for limiting the results of ResourcesDBClient.ListResourceDocs.
type DBClientListResourceDocsOptions struct {
	// ResourceType matches (case-insensitively) the Azure resource type. If unspecified,
	// ResourcesDBClient.ListResourceDocs will match resource documents for any resource type.
	ResourceType *azcorearm.ResourceType

	// PageSizeHint can limit the number of items returned at once. A negative value will cause
	// the returned iterator to yield all matching documents (same as leaving the option nil).
	// A positive value will cause the returned iterator to include a continuation token if
	// additional items are available.
	PageSizeHint *int32

	// ContinuationToken can be supplied when limiting the number of items returned at once
	// through PageSizeHint.
	ContinuationToken *string
}

// ChangeFeedClient is the narrow interface consumed by ChangeFeedListWatcher.
// Any Cosmos container that exposes its change feed satisfies it — both the
// shared "Resources" container and per-management-cluster kube-applier containers.
type ChangeFeedClient interface {
	ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error)
	ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error)
}
