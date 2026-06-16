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

package framework

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestIsResourceGroupNotFoundError(t *testing.T) {
	t.Parallel()

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
			err:  fmt.Errorf("something went wrong"),
			want: false,
		},
		{
			name: "HTTP 404 status",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "ResourceGroupNotFound error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"},
			want: true,
		},
		{
			name: "ResourceNotFound error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceNotFound"},
			want: true,
		},
		{
			name: "HTTP 409 Conflict without matching error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ConflictError"},
			want: false,
		},
		{
			name: "HTTP 403 Forbidden",
			err:  &azcore.ResponseError{StatusCode: http.StatusForbidden},
			want: false,
		},
		{
			name: "wrapped ResponseError with 404",
			err:  fmt.Errorf("outer: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}),
			want: true,
		},
		{
			name: "wrapped ResponseError with ResourceGroupNotFound",
			err:  fmt.Errorf("outer: %w", &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"}),
			want: true,
		},
		{
			name: "wrapped non-ResponseError",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isResourceGroupNotFoundError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsIgnorableResourceGroupCleanupError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error returns false",
			err:  nil,
			want: false,
		},
		{
			name: "non-ResponseError returns false",
			err:  fmt.Errorf("some error"),
			want: false,
		},
		{
			name: "404 ResponseError returns true",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "ResourceGroupNotFound returns true",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"},
			want: true,
		},
		{
			name: "joined error with all not-found errors",
			err: errors.Join(
				&azcore.ResponseError{StatusCode: http.StatusNotFound},
				&azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"},
			),
			want: true,
		},
		{
			name: "joined error with one real failure and one not-found",
			err: errors.Join(
				&azcore.ResponseError{StatusCode: http.StatusNotFound},
				&azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"},
			),
			want: false,
		},
		{
			name: "joined error with all real failures",
			err: errors.Join(
				&azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"},
				&azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "SomeOtherError"},
			),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isIgnorableResourceGroupCleanupError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
