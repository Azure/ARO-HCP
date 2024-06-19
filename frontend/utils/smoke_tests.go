package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	apiURL           = "http://localhost:8443"
	testSubscription = "00000000-0000-0000-0000-000000000000"
	fail             = "FAILED"
	pass             = "PASSED"
)

// ============================DEFINE SMOKE TESTS HERE=============================
var testSuite = smokeTests{
	smokeTest{
		name:       "Register Subscription Smoke Test",
		action:     "Registering a subscription with provider",
		method:     http.MethodPut,
		resourceID: fmt.Sprintf("/subscriptions/%s?api-version=2.0", testSubscription),
		payload:    strings.NewReader(fmt.Sprintf(`{"state":"Registered", "properties": { "tenantId": "%s"}}`, testSubscription)),
	},

	smokeTest{
		name:       "Get Subscription Smoke Test",
		action:     "Getting a subscription",
		method:     http.MethodGet,
		resourceID: fmt.Sprintf("/subscriptions/%s?api-version=2.0", testSubscription),
		payload:    strings.NewReader(""),
	},

	smokeTest{
		name:       "Unregister Subscription Smoke Test",
		action:     "Unregistering a subscription with provider",
		method:     http.MethodPut,
		resourceID: fmt.Sprintf("/subscriptions/%s?api-version=2.0", testSubscription),
		payload:    strings.NewReader(fmt.Sprintf(`{"state":"Unegistered", "properties": { "tenantId": "%s"}}`, testSubscription)),
	},
}

// ================================================================================

type smokeTests []smokeTest

type smokeTest struct {
	name       string
	action     string
	method     string
	resourceID string
	payload    *strings.Reader
}

type smokeTestResult struct {
	test       smokeTest
	result     string
	statusCode int
	output     string
}

type smokeTestRunner struct {
	ctx         context.Context
	client      http.Client
	resp        *http.Response
	testSuite   smokeTests
	testResults []smokeTestResult
}

func newRunner() *smokeTestRunner {
	return &smokeTestRunner{
		ctx:         context.Background(),
		client:      http.Client{},
		resp:        &http.Response{},
		testSuite:   testSuite,
		testResults: []smokeTestResult{},
	}
}

func (s *smokeTestRunner) runSmokeTests() ([]smokeTestResult, error) {
	for _, test := range s.testSuite {
		testResult := smokeTestResult{test: test}

		req, err := http.NewRequest(test.method, apiURL+test.resourceID, test.payload)
		if err != nil {
			testResult.result = fail
			testResult.output = fmt.Sprint(err)
			s.testResults = append(s.testResults, testResult)
			continue
		}
		req.Header.Set("Content-type", "application/json")

		s.resp, err = s.client.Do(req)
		if err != nil {
			testResult.result = fail
			testResult.output = fmt.Sprint(err)
			testResult.statusCode = s.resp.StatusCode
			s.testResults = append(s.testResults, testResult)
			continue
		}
		testResult.statusCode = s.resp.StatusCode

		body, err := io.ReadAll(s.resp.Body)
		if err != nil {
			testResult.result = fail
			testResult.output = fmt.Sprint(err)
			s.testResults = append(s.testResults, testResult)
			continue
		}

		if s.resp.StatusCode > 299 {
			testResult.result = fail
		} else {
			testResult.result = pass
		}
		testResult.output = string(body)
		s.testResults = append(s.testResults, testResult)

		err = s.resp.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	return s.testResults, nil
}

func (s *smokeTestRunner) getTestDocument() (map[string]interface{}, error) {
	var document map[string]interface{}
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

	container, err := client.NewContainer(dbName, "Subscriptions")
	if err != nil {
		return nil, err
	}

	opt := azcosmos.QueryOptions{
		QueryParameters: []azcosmos.QueryParameter{{Name: "@partitionKey", Value: testSubscription}},
	}
	pk := azcosmos.NewPartitionKeyString(testSubscription)
	queryPager := container.NewQueryItemsPager("SELECT * FROM c WHERE c.partitionKey = @partitionKey", pk, &opt)
	for queryPager.More() {
		queryResponse, err := queryPager.NextPage(s.ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range queryResponse.Items {
			err = json.Unmarshal(item, &document)
			if err != nil {
				return nil, err
			}
		}
	}
	if document != nil {
		return document, nil
	}
	return nil, err
}

func (s *smokeTestRunner) cleanup(docID string) error {
	return nil
}

func main() {
	logs := log.Default()
	logs.SetFlags(0)

	logs.Println("Starting tests...")
	ts := newRunner()
	results, err := ts.runSmokeTests()
	if err != nil {
		logs.Printf("Running the test suite failed: %v\n", err)
	}

	for _, result := range results {
		logs.Printf("Test: %s\nDescription: %s\nResult: %s\nStatusCode: %d\nOutput: %s\n\n",
			result.test.name,
			result.test.action,
			result.result,
			result.statusCode,
			result.output)
	}

	doc, err := ts.getTestDocument()
	if err != nil {
		logs.Printf("get doc failed: %v", err)
	}
	fmt.Printf(" DOC: %+v", doc)
}
