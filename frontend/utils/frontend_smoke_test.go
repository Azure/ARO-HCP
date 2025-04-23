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

//go:build smoke_test
// +build smoke_test

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	apiURL            = "http://localhost:8443"
	testSubscription  = "00000000-0000-0000-0000-000000000000"
	testSubResourceID = "/subscriptions/" + testSubscription + "?api-version=2.0"
)

func TestPutSubscriptions(t *testing.T) {
	testSuite := smokeTests{
		smokeTest{
			name:       "Register Subscription",
			action:     "Registering a subscription with provider",
			method:     http.MethodPut,
			resourceID: testSubResourceID,
			payload:    strings.NewReader(fmt.Sprintf(`{"state":"Registered", "registrationDate": "now", "properties": { "tenantId": "%s"}}`, testSubscription)),
			expect:     "{\"state\":\"Registered\",\"registrationDate\":\"now\",\"properties\":{\"tenantId\":\"00000000-0000-0000-0000-000000000000\"}}",
		},

		smokeTest{
			name:       "Unregister Subscription",
			action:     "Unregistering a subscription with provider",
			method:     http.MethodPut,
			resourceID: testSubResourceID,
			payload:    strings.NewReader(fmt.Sprintf(`{"state":"Unregistered", "registrationDate":"now", "properties": { "tenantId": "%s"}}`, testSubscription)),
			expect:     "{\"state\":\"Unregistered\",\"registrationDate\":\"now\",\"properties\":{\"tenantId\":\"00000000-0000-0000-0000-000000000000\"}}",
		},
	}

	runner := newRunner()
	for _, test := range testSuite {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, apiURL+test.resourceID, test.payload)
			require.NoError(t, err)
			req.Header.Set("Content-type", "application/json")

			resp, err := runner.client.Do(req)
			require.NoError(t, err)

			if assert.Greater(t, resp.StatusCode, 299) {
				assert.True(t, t.Run("PostTestValidation", runner.testValidation(t, test)))
			}
		})
	}
}

// smokeTest captures the data needed to test the functionality of the RP
type smokeTest struct {
	name       string
	action     string
	method     string
	resourceID string
	payload    *strings.Reader
	expect     string
}

type smokeTests []smokeTest

// smokeTestRunner is a constructor to instantiate everything needed for testing including clean up
type smokeTestRunner struct {
	ctx    context.Context
	client http.Client
}

// newRunner returns a new smokeTestRunner
func newRunner() *smokeTestRunner {
	return &smokeTestRunner{
		ctx:    context.Background(),
		client: http.Client{},
	}
}

// testValidation is a post validation test ran after each test
// it performs a GET on the RP to ensure changes made by a PUT are validated as accurate
func (s *smokeTestRunner) testValidation(t *testing.T, test smokeTest) func(t *testing.T) {
	return func(t *testing.T) {
		resp, err := s.client.Get(apiURL + testSubResourceID)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, test.expect, string(body))
	}
}
