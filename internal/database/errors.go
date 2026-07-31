// Copyright 2026 Microsoft Corporation
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

package database

import (
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TransactionStepError is returned when a Cosmos DB transactional batch step fails
// with a non-424 status code. It carries the HTTP status so helpers like
// IsPreconditionFailedError can recognize it via errors.As.
type TransactionStepError struct {
	// Step is the 1-based index of the failed operation within the batch.
	Step int
	// TotalSteps is the number of operations in the batch.
	TotalSteps int
	// HTTPStatusCode is the HTTP status returned for the failed step (e.g. 412).
	HTTPStatusCode int
	// StepDetails describes the failed operation (action type, resource ID, etag, etc.).
	StepDetails CosmosDBTransactionStepDetails
}

// NewTransactionStepError returns an error that indicates that a step in a Cosmos
// DB transaction failed with the given HTTP status code.
func NewTransactionStepError(step, totalSteps, httpStatusCode int, details CosmosDBTransactionStepDetails) error {
	return &TransactionStepError{
		Step:           step,
		TotalSteps:     totalSteps,
		HTTPStatusCode: httpStatusCode,
		StepDetails:    details,
	}
}

func (e *TransactionStepError) Error() string {
	return fmt.Sprintf(
		"transaction step %d of %d (%s %s on %s, etag %q) failed with %d %s",
		e.Step, e.TotalSteps,
		e.StepDetails.ActionType, e.StepDetails.GoType, e.StepDetails.ResourceID, string(e.StepDetails.Etag),
		e.HTTPStatusCode, http.StatusText(e.HTTPStatusCode),
	)
}

func NewNotFoundError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "404 Not Found",
		StatusCode: http.StatusNotFound,
	}
}
