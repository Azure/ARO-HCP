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
			payload:    strings.NewReader(fmt.Sprintf(`{"state":"Registered", "properties": { "tenantId": "%s"}}`, testSubscription)),
			expect:     "{\"state\":\"Registered\",\"properties\":{\"tenantId\":\"00000000-0000-0000-0000-000000000000\"}}",
		},

		smokeTest{
			name:       "Unregister Subscription",
			action:     "Unregistering a subscription with provider",
			method:     http.MethodPut,
			resourceID: testSubResourceID,
			payload:    strings.NewReader(fmt.Sprintf(`{"state":"Unregistered", "properties": { "tenantId": "%s"}}`, testSubscription)),
			expect:     "{\"state\":\"Unregistered\",\"properties\":{\"tenantId\":\"00000000-0000-0000-0000-000000000000\"}}",
		},
	}

	runner := newRunner()
	for _, test := range testSuite {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, apiURL+test.resourceID, test.payload)
			if err != nil {
				t.Fatalf("failed to create new request: %v", err)
			}
			req.Header.Set("Content-type", "application/json")

			resp, err := runner.client.Do(req)
			if err != nil {
				t.Fatalf("failed to make http request: %v", err)
			}

			if resp.StatusCode > 299 {
				t.Fatalf("failed request, status code %d", resp.StatusCode)
			}

			validated := t.Run("PostTestValidation", runner.testValidation(t, test))
			if !validated {
				t.Fatal()
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
		if err != nil {
			t.Fatalf("post test validation failed: could not get the subscription doc: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			t.Fatalf("post test validation failed: error reading body: %v", err)
		}
		if string(body) != test.expect {
			t.Errorf("post test validation failed: expected %s, got %s", test.expect, string(body))
		}
	}
}
