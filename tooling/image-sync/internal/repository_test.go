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

package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"gotest.tools/v3/assert"
)

func TestNewQuayClientAssertAuthentication(t *testing.T) {
	client := NewQuayRegistry(&SyncConfig{RequestTimeout: 1, NumberOfTags: 1}, "fooBar")

	assert.Assert(t, client != nil)

	mock := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer fooBar", r.Header.Get("Authorization"))
			_, err := w.Write([]byte(`{"tags":[{"name":"test"}]}`))
			assert.NilError(t, err)
		}))
	defer mock.Close()

	client.baseUrl = mock.URL

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/test", mock.URL), nil)
	assert.NilError(t, err)

	resp, err := client.httpclient.Do(req)
	assert.NilError(t, err)

	assert.Equal(t, 200, resp.StatusCode)
}

func TestQuayGetTags(t *testing.T) {
	q := QuayRegistry{}
	q.numberOftags = 3

	testcases := []struct {
		name          string
		responses     []string
		length        int
		expected      []string
		expectedError bool
		errorString   string
		statusCode    int
	}{
		{
			name:          "one response",
			responses:     []string{`{"tags":[{"name":"test"}]}`},
			length:        1,
			expected:      []string{"test"},
			expectedError: false,
		},
		{
			name:          "multiple response",
			responses:     []string{`{"tags":[{"name":"test"},{"name":"test"},{"name":"test"},{"name":"test"},{"name":"test"} ]}`},
			length:        3,
			expected:      []string{"test", "test", "test"},
			expectedError: false,
		},
		{
			name:          "fail",
			responses:     []string{`{"tags":[{"name":"test"},]}`},
			expectedError: true,
			errorString:   "failed to get tags: failed to unmarshal response: invalid character ']' looking for beginning of value",
		},
		{
			name:          "httpFail",
			expectedError: true,
			responses:     []string{""},
			errorString:   "failed to get tags: unexpected status code 502",
			statusCode:    http.StatusBadGateway,
		},
		{
			name: "paginated",
			responses: []string{`{"tags":[{"name":"test0"}, {"name":"test1"}], "has_additional": true}`,
				`{"tags":[{"name":"test2"}], "has_additional": true}`,
				`{"tags":[{"name":"test3"}], "has_additional": true}`},
			length:   3,
			expected: []string{"test0", "test1", "test2"},
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					page, err := strconv.Atoi(r.URL.Query().Get("page"))
					assert.NilError(t, err)
					if testcase.statusCode != 0 {
						w.WriteHeader(testcase.statusCode)
					}
					_, err = w.Write([]byte(testcase.responses[page-1]))
					assert.NilError(t, err)
				}))
			defer mock.Close()

			q.baseUrl = mock.URL
			q.httpclient = mock.Client()

			quayTags, err := q.GetTags(context.TODO(), "test")
			if testcase.expectedError {
				assert.Error(t, err, testcase.errorString)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, testcase.length, len(quayTags))
				for i, tag := range quayTags {
					assert.Equal(t, testcase.expected[i], tag)
				}
			}
		})
	}
}

func TestGetPullSecret(t *testing.T) {
	acr := AzureContainerRegistry{
		tenantId:   "test",
		credential: &azidentity.DefaultAzureCredential{},

		getAccessTokenImpl: func(ctx context.Context, dac azcore.TokenCredential) (string, error) {
			return "fooBar", nil
		},
		getACRUrlImpl: func(acrName string) string {
			return acrName
		},
	}

	testcases := []struct {
		name          string
		response      string
		expectedError bool
		errorString   string
		statusCode    int
	}{
		{
			name:          "success",
			response:      `{"refresh_token":"fooBar"}`,
			expectedError: false,
		},
		{
			name:          "fail",
			response:      `{"refresh_token":"`,
			expectedError: true,
			errorString:   "failed to unmarshal response: unexpected end of JSON input",
		},
		{
			name:          "httpFail",
			expectedError: true,
			errorString:   "unexpected status code 502",
			statusCode:    http.StatusBadGateway,
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if testcase.statusCode != 0 {
						w.WriteHeader(testcase.statusCode)
					}
					_, err := w.Write([]byte(testcase.response))
					assert.NilError(t, err)
				}))
			defer mock.Close()

			acr.httpClient = mock.Client()
			acr.acrName = mock.URL
			authSecret, err := acr.GetPullSecret(context.TODO())
			if testcase.expectedError {
				assert.Error(t, err, testcase.errorString)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, "fooBar", authSecret.RefreshToken)
			}
		})
	}
}

func TestCreateOauthRequest(t *testing.T) {
	acr := AzureContainerRegistry{
		acrName:  "test",
		tenantId: "test",
		getACRUrlImpl: func(acrName string) string {
			return acrName
		},
	}

	req, err := acr.createOauthRequest(context.TODO(), "fooBar")
	assert.NilError(t, err)

	bodyBytes, err := io.ReadAll(req.Body)
	assert.NilError(t, err)
	body := string(bodyBytes)

	assert.Equal(t, "access_token=fooBar&grant_type=access_token&service=test&tenant=test", body)
	assert.Equal(t, "POST", req.Method)
	assert.Equal(t, "test/oauth2/exchange/", req.URL.Path)
	assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))
}

func TestGetNewestTags(t *testing.T) {

	testCases := []struct {
		name                string
		response            *rawOCIResponse
		numberOfTags        int
		expected            []string
		expectedError       bool
		expectedErrorString string
	}{
		{
			name: "parser error",
			response: &rawOCIResponse{
				Manifest: map[string]rawManifest{
					"latest": {
						TimeUploadedMs: "1x",
						Tag:            []string{"latest"},
					},
				},
			},
			expectedError:       true,
			expectedErrorString: "failed to parse manifest {1x [latest]} time: strconv.Atoi: parsing \"1x\": invalid syntax",
		},
		{
			name: "single tag",
			response: &rawOCIResponse{
				Manifest: map[string]rawManifest{
					"latest": {
						TimeUploadedMs: "1",
						Tag:            []string{"latest"},
					},
				},
				Tags: []string{"latest"},
			},
			numberOfTags: 1,
			expected:     []string{"latest"},
		},
		{
			name: "multiple tags",
			response: &rawOCIResponse{
				Manifest: map[string]rawManifest{
					"abc": {
						TimeUploadedMs: "0",
						Tag:            []string{"abc"},
					},
					"def": {
						TimeUploadedMs: "1",
						Tag:            []string{"def"},
					},
					"ghi": {
						TimeUploadedMs: "2",
						Tag:            []string{"ghi"},
					},
				},
				Tags: []string{"abc", "def", "ghi"},
			},
			numberOfTags: 1,
			expected:     []string{"ghi"},
		},
		{
			name: "complete tags",
			response: &rawOCIResponse{
				Manifest: map[string]rawManifest{
					"abc": {
						TimeUploadedMs: "0",
						Tag:            []string{"abc"},
					},
					"def": {
						TimeUploadedMs: "1",
						Tag:            []string{"def"},
					},
					"ghi": {
						TimeUploadedMs: "2",
						Tag:            []string{"ghi"},
					},
				},
				Tags: []string{"abc", "def", "ghi"},
			},
			numberOfTags: 3,
			expected:     []string{"ghi", "def", "abc"},
		},
		{
			name: "untagged manifest",
			response: &rawOCIResponse{
				Manifest: map[string]rawManifest{
					"abc": {
						TimeUploadedMs: "0",
					},
					"def": {
						TimeUploadedMs: "1",
						Tag:            []string{"def"},
					},
					"ghi": {
						TimeUploadedMs: "2",
						Tag:            []string{"ghi"},
					},
				},
				Tags: []string{"def", "ghi"},
			},
			numberOfTags: 2,
			expected:     []string{"ghi", "def"},
		},
	}

	for _, testcase := range testCases {
		t.Run(testcase.name, func(t *testing.T) {
			tags, err := getNewestTags(testcase.response, testcase.numberOfTags)
			if testcase.expectedError {
				assert.Error(t, err, testcase.expectedErrorString)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, testcase.numberOfTags, len(tags))
				for i, tag := range tags {
					assert.Equal(t, testcase.expected[i], tag)
				}
			}
		})

	}

}

func TestOciGetTags(t *testing.T) {
	o := OCIRegistry{}
	o.numberOftags = 3

	testcases := []struct {
		name          string
		response      string
		length        int
		expected      []string
		expectedError bool
		errorString   string
		statusCode    int
	}{
		{
			name:          "one response",
			response:      `{"manifest":{"latest":{"timeUploadedMs":"1","tag":["latest"]}},"tags":["latest"]}`,
			length:        1,
			expected:      []string{"latest"},
			expectedError: false,
		},
		{
			name:          "fail",
			response:      `{"manifest":{"latest":{"timeUploadedMs":"1x","tag":["latest"]}},"tags":["latest"]}`,
			expectedError: true,
			errorString:   "failed to parse manifest {1x [latest]} time: strconv.Atoi: parsing \"1x\": invalid syntax",
		},
		{
			name:          "httpFail",
			expectedError: true,
			errorString:   "unexpected status code 502",
			statusCode:    http.StatusBadGateway,
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			mock := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if testcase.statusCode != 0 {
						w.WriteHeader(testcase.statusCode)
					}
					_, err := w.Write([]byte(testcase.response))
					assert.NilError(t, err)
				}))
			defer mock.Close()

			o.baseURL = mock.URL
			o.httpclient = mock.Client()

			ociTags, err := o.GetTags(context.TODO(), "test")
			if testcase.expectedError {
				assert.Error(t, err, testcase.errorString)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, testcase.length, len(ociTags))
				for i, tag := range ociTags {
					assert.Equal(t, testcase.expected[i], tag)
				}
			}
		})
	}
}
