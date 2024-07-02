package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"gotest.tools/v3/assert"
)

func TestNewQuayClientAssertAuthentication(t *testing.T) {
	client := NewQuayRegistry(&SyncConfig{RequestTimeout: 1, NumberOfTags: 1}, "fooBar")

	assert.Assert(t, client != nil)

	mock := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer fooBar", r.Header.Get("Authorization"))
			w.Write([]byte(`{"tags":[{"name":"test"}]}`))
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
		response      string
		length        int
		expected      []string
		expectedError bool
		errorString   string
		statusCode    int
	}{
		{
			name:          "one response",
			response:      `{"tags":[{"name":"test"}]}`,
			length:        1,
			expected:      []string{"test"},
			expectedError: false,
		},
		{
			name:          "multiple response",
			response:      `{"tags":[{"name":"test"},{"name":"test"},{"name":"test"},{"name":"test"},{"name":"test"} ]}`,
			length:        3,
			expected:      []string{"test", "test", "test"},
			expectedError: false,
		},
		{
			name:          "fail",
			response:      `{"tags":[{"name":"test"},]}`,
			expectedError: true,
			errorString:   "failed to unmarshal response: invalid character ']' looking for beginning of value",
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
					w.Write([]byte(testcase.response))
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

		getAccessTokenImpl: func(ctx context.Context, dac *azidentity.DefaultAzureCredential) (string, error) {
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
					w.Write([]byte(testcase.response))
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
