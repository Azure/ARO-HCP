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

package main

import (
	"fmt"
	"strings"

	"github.com/onsi/gomega/format"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func configureGomegaFormatting() {
	// Retain complete failure messages and Gomega's default depth handling.
	format.MaxLength = 0
	format.RegisterCustomFormatter(formatAzureError)
}

func formatAzureError(value any) (string, bool) {
	err, ok := value.(error)
	if !ok {
		return "", false
	}
	var output strings.Builder
	seen := map[*azcore.ResponseError]bool{}
	var visit func(error)
	visit = func(err error) {
		switch err := err.(type) {
		case *azcore.ResponseError:
			if err == nil || seen[err] {
				return
			}
			seen[err] = true
			if output.Len() == 0 {
				output.WriteString("<Azure error internals omitted>\nAzure request IDs:")
			}
			method, uri := "unavailable", "unavailable"
			var correlationID, clientRequestID string
			if response := err.RawResponse; response != nil {
				correlationID = response.Header.Get(coreapi.HeaderNameCorrelationRequestID)
				clientRequestID = response.Header.Get(coreapi.HeaderNameClientRequestID)
				if request := response.Request; request != nil {
					if request.Method != "" {
						method = request.Method
					}
					if request.URL != nil {
						// Match the SDK's error URI without credentials, query parameters, or fragments.
						uri = fmt.Sprintf("%s://%s%s", request.URL.Scheme, request.URL.Host, request.URL.EscapedPath())
					}
					if correlationID == "" {
						correlationID = request.Header.Get(coreapi.HeaderNameCorrelationRequestID)
					}
					if clientRequestID == "" {
						clientRequestID = request.Header.Get(coreapi.HeaderNameClientRequestID)
					}
				}
			}
			if correlationID == "" {
				correlationID = "unavailable"
			}
			if clientRequestID == "" {
				clientRequestID = "unavailable"
			}
			fmt.Fprintf(&output, "\n  %s %s\n    correlation ID: %s\n    client request ID: %s", method, uri, correlationID, clientRequestID)
		case interface{ Unwrap() []error }:
			for _, child := range err.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(err.Unwrap())
		}
	}
	// Walk every joined branch, not just errors.As's first match. Gomega already
	// prints the outer Error() message; do not repeat it or reflect HTTP internals.
	visit(err)
	return output.String(), output.Len() > 0
}
