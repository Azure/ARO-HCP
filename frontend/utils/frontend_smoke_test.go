//go:build smoke_test
// +build smoke_test

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"io"
	"net/http"
	"os"
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

	runner, err := newRunner()
	if err != nil {
		t.Fatal(err)
	}

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

	err = runner.cleanup()
	if err != nil {
		t.Fatal(err)
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
	ctx      context.Context
	client   http.Client
	dbClient *azcosmos.ContainerClient
}

// newRunner returns a new smokeTestRunner
func newRunner() (*smokeTestRunner, error) {
	dbName := os.Getenv("DB_NAME")
	dbURL := os.Getenv("DB_URL")

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}

	client, err := azcosmos.NewClient(dbURL, cred, nil)
	if err != nil {
		return nil, err
	}

	cClient, err := client.NewContainer(dbName, "Subscriptions")
	if err != nil {
		return nil, err
	}

	return &smokeTestRunner{
		ctx:      context.Background(),
		client:   http.Client{},
		dbClient: cClient,
	}, nil
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

// getTestDocument fetches the document ID of the test document in Cosmos for clean up
func (s *smokeTestRunner) getTestDocumentID() (string, error) {
	var document map[string]interface{}

	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{{Name: "@partitionKey", Value: testSubscription}},
	}
	pk := azcosmos.NewPartitionKeyString(testSubscription)
	queryPager := s.dbClient.NewQueryItemsPager("SELECT * FROM c WHERE c.partitionKey = @partitionKey", pk, &opt)
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(s.ctx)
		if err != nil {
			return "", err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &document)
			if err != nil {
				return "", err
			}
		}
	}
	if document != nil {
		return fmt.Sprintf("%s", document["id"]), nil
	}
	return "", fmt.Errorf("document not found -- nothing to do")
}

// cleanup ensures the test document is removed from Cosmos for future testing
func (s *smokeTestRunner) cleanup() error {
	var document map[string]interface{}

	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{{Name: "@partitionKey", Value: testSubscription}},
	}
	pk := azcosmos.NewPartitionKeyString(testSubscription)
	queryPager := s.dbClient.NewQueryItemsPager("SELECT * FROM c WHERE c.partitionKey = @partitionKey", pk, &opt)
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(s.ctx)
		if err != nil {
			return err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &document)
			if err != nil {
				return err
			}
		}
	}
	if document != nil {
		_, err := s.dbClient.DeleteItem(s.ctx, pk, fmt.Sprint(document["id"]), nil)
		if err != nil {
			return err
		}

	} else {
		return fmt.Errorf("document not found -- nothing to do")
	}
	return nil
}
