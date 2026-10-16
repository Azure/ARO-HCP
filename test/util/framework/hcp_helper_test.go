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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestIsAPINotDeployedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "not found", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, want: true},
		{name: "provider not registered", err: &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "NoRegisteredProviderFound"}, want: true},
		{name: "bad request", err: &azcore.ResponseError{StatusCode: http.StatusBadRequest}},
		{name: "other error", err: errors.New("not deployed")},
		{name: "nil error"},
		{name: "wrapped response error", err: fmt.Errorf("create cluster: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, IsAPINotDeployedError(test.err))
		})
	}
}

func TestIsFeatureNotDeployedError(t *testing.T) {
	const field = "properties.platform.vnetIntegrationSubnetId"
	const matchingBody = `{"error":{"code":"InvalidRequestContent","target":"properties.platform.vnetIntegrationSubnetId"}}`
	newResponseError := func(status int, body string, closed bool) error {
		responseBody := &featureErrorBody{Reader: strings.NewReader(body)}
		responseError := &azcore.ResponseError{
			StatusCode: status,
			RawResponse: &http.Response{
				StatusCode: status,
				Body:       responseBody,
			},
		}
		if closed {
			_ = responseError.Error()
			responseError.RawResponse.Body = responseBody
			require.NoError(t, responseBody.Close())
		}
		return responseError
	}
	tests := []struct {
		name  string
		err   error
		field string
		want  bool
	}{
		{name: "matching target", err: newResponseError(http.StatusBadRequest, matchingBody, false), field: field, want: true},
		{name: "different target", err: newResponseError(http.StatusBadRequest, `{"error":{"code":"InvalidRequestContent","target":"properties.platform.subnetId","message":"properties.platform.vnetIntegrationSubnetId"}}`, false), field: field},
		{name: "closed body matching field", err: newResponseError(http.StatusBadRequest, matchingBody, true), field: field, want: true},
		{name: "closed body missing field", err: newResponseError(http.StatusBadRequest, `{"error":{"code":"InvalidRequestContent","target":"properties.platform.subnetId"}}`, true), field: field},
		{name: "target case mismatch", err: newResponseError(http.StatusBadRequest, `{"error":{"code":"InvalidRequestContent","target":"Properties.Platform.VnetIntegrationSubnetId"}}`, false), field: field, want: true},
		{name: "closed body case mismatch", err: newResponseError(http.StatusBadRequest, `{"error":{"code":"INVALIDREQUESTCONTENT","target":"Properties.Platform.VnetIntegrationSubnetId"}}`, true), field: field, want: true},
		{name: "closed body missing code", err: newResponseError(http.StatusBadRequest, `{"error":{"code":"InvalidParameter","target":"properties.platform.vnetIntegrationSubnetId"}}`, true), field: field},
		{name: "different code", err: newResponseError(http.StatusBadRequest, `{"error":{"code":"InvalidParameter","target":"properties.platform.vnetIntegrationSubnetId"}}`, false), field: field},
		{name: "unparseable body matching field", err: newResponseError(http.StatusBadRequest, "InvalidRequestContent: Properties.Platform.VnetIntegrationSubnetId", false), field: field, want: true},
		{name: "unparseable body missing field", err: newResponseError(http.StatusBadRequest, "InvalidRequestContent", false), field: field},
		{name: "not found", err: newResponseError(http.StatusNotFound, matchingBody, false), field: field},
		{name: "empty field", err: newResponseError(http.StatusBadRequest, matchingBody, false)},
		{name: "missing response", err: &azcore.ResponseError{StatusCode: http.StatusBadRequest}, field: field},
		{name: "missing body", err: &azcore.ResponseError{StatusCode: http.StatusBadRequest, RawResponse: &http.Response{StatusCode: http.StatusBadRequest}}, field: field},
		{name: "nil error", field: field},
		{name: "nil response error", err: (*azcore.ResponseError)(nil), field: field},
		{name: "other error", err: errors.New("InvalidRequestContent: " + field), field: field},
		{name: "wrapped response error", err: fmt.Errorf("create cluster: %w", newResponseError(http.StatusBadRequest, matchingBody, false)), field: field, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var responseError *azcore.ResponseError
			var originalBody *featureErrorBody
			var originalBytes []byte
			if errors.As(test.err, &responseError) && responseError != nil && responseError.RawResponse != nil {
				originalBody, _ = responseError.RawResponse.Body.(*featureErrorBody)
				if originalBody != nil && !originalBody.closed {
					var err error
					originalBytes, err = io.ReadAll(originalBody)
					require.NoError(t, err)
					originalBody.Reader = strings.NewReader(string(originalBytes))
				}
			}
			assert.Equal(t, test.want, IsFeatureNotDeployedError(test.err, test.field))
			if originalBytes != nil && responseError.StatusCode == http.StatusBadRequest && test.field != "" {
				assert.True(t, originalBody.closed, "original response body must be closed")
				restoredBytes, err := io.ReadAll(responseError.RawResponse.Body)
				require.NoError(t, err)
				assert.Equal(t, originalBytes, restoredBytes, "response body must be restored")
			}
			if responseError != nil && responseError.RawResponse != nil && responseError.RawResponse.Body != nil {
				require.NoError(t, responseError.RawResponse.Body.Close())
			}
		})
	}
}

type featureErrorBody struct {
	io.Reader
	closed bool
}

func (body *featureErrorBody) Read(buffer []byte) (int, error) {
	if body.closed {
		return 0, http.ErrBodyReadAfterClose
	}
	return body.Reader.Read(buffer)
}

func (body *featureErrorBody) Close() error {
	body.closed = true
	return nil
}
