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

package framework

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
)

func TestClassifyListBySubscriptionProbeError(t *testing.T) {
	cause := fmt.Errorf("dial: %w", io.EOF)
	responseError := func(status int, code string) error {
		return &azcore.ResponseError{StatusCode: status, ErrorCode: code}
	}
	tests := []struct {
		name       string
		err        error
		wantServed bool
		wantErr    bool
	}{
		{name: "nil error is served", err: nil, wantServed: true},
		{name: "400 InvalidResourceType is not served", err: responseError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidResourceType)},
		{name: "400 NoRegisteredProviderFound is not served", err: responseError(http.StatusBadRequest, "NoRegisteredProviderFound")},
		{name: "404 SubscriptionNotFound is an error", err: responseError(http.StatusNotFound, coreapi.CloudErrorCodeSubscriptionNotFound), wantErr: true},
		{name: "400 InvalidSubscriptionID is an error", err: responseError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidSubscriptionID), wantErr: true},
		{name: "409 InvalidSubscriptionState is an error", err: responseError(http.StatusConflict, coreapi.CloudErrorCodeInvalidSubscriptionState), wantErr: true},
		{name: "bare 404 is an error", err: responseError(http.StatusNotFound, ""), wantErr: true},
		{name: "404 ResourceNotFound is an error", err: responseError(http.StatusNotFound, coreapi.CloudErrorCodeResourceNotFound), wantErr: true},
		{name: "400 InvalidParameter is an error", err: responseError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidParameter), wantErr: true},
		{name: "404 NoRegisteredProviderFound is an error", err: responseError(http.StatusNotFound, "NoRegisteredProviderFound"), wantErr: true},
		{name: "500 NoRegisteredProviderFound is an error", err: responseError(http.StatusInternalServerError, "NoRegisteredProviderFound"), wantErr: true},
		{name: "401 is an error", err: responseError(http.StatusUnauthorized, ""), wantErr: true},
		{name: "403 is an error", err: responseError(http.StatusForbidden, ""), wantErr: true},
		{name: "500 is an error", err: responseError(http.StatusInternalServerError, ""), wantErr: true},
		{name: "wrapped transport error is an error", err: cause, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			served, err := ClassifyListBySubscriptionProbeError(tt.err)
			if served != tt.wantServed {
				t.Errorf("ClassifyListBySubscriptionProbeError() served = %v, want %v", served, tt.wantServed)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("ClassifyListBySubscriptionProbeError() error = %v, want an error: %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, tt.err) {
				t.Errorf("ClassifyListBySubscriptionProbeError() error = %v, must wrap %v", err, tt.err)
			}
		})
	}
	if _, err := ClassifyListBySubscriptionProbeError(cause); !errors.Is(err, io.EOF) {
		t.Errorf("ClassifyListBySubscriptionProbeError() error = %v, must wrap io.EOF", err)
	}
}
