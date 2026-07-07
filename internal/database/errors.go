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

// transactionStepError is returned when a Cosmos DB transactional batch step fails
// with a non-424 status code. It carries the HTTP status so helpers like
// IsPreconditionFailedError can recognize it via errors.As.
type transactionStepError struct {
	// step is the 1-based index of the failed operation within the batch.
	step int
	// totalSteps is the number of operations in the batch.
	totalSteps int
	// httpStatusCode is the HTTP status returned for the failed step (e.g. 412).
	httpStatusCode int
}

// NewTransactionStepError returns an error that indicates that a step in a Cosmos
// DB transaction failed with the given HTTP status code.
func NewTransactionStepError(step, totalSteps, httpStatusCode int) error {
	return &transactionStepError{
		step:           step,
		totalSteps:     totalSteps,
		httpStatusCode: httpStatusCode,
	}
}

func (e *transactionStepError) Error() string {
	return fmt.Sprintf(
		"transaction step %d of %d failed with %d %s",
		e.step, e.totalSteps, e.httpStatusCode, http.StatusText(e.httpStatusCode),
	)
}

func NewNotFoundError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "404 Not Found",
		StatusCode: http.StatusNotFound,
	}
}
