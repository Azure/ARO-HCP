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
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestIsAPINotDeployedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "non-ResponseError",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "HTTP 404",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "NoRegisteredProviderFound",
			err:  &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "NoRegisteredProviderFound"},
			want: true,
		},
		{
			name: "HTTP 400 InvalidResourceType (unsupported API version)",
			err:  &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: coreapi.CloudErrorCodeInvalidResourceType},
			want: true,
		},
		{
			name: "HTTP 400 with unrelated error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "SomeOtherError"},
			want: false,
		},
		{
			name: "HTTP 500 server error",
			err:  &azcore.ResponseError{StatusCode: http.StatusInternalServerError, ErrorCode: "InternalError"},
			want: false,
		},
		{
			name: "HTTP 409 conflict",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAPINotDeployedError(tt.err)
			if got != tt.want {
				t.Errorf("IsAPINotDeployedError() = %v, want %v", got, tt.want)
			}
		})
	}
}
