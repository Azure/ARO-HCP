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

package controllerutils

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
)

// fakeControllerCRUD is a simple in-memory implementation of ResourceCRUD[coreapi.Controller, *coreapi.Controller] for testing
type fakeControllerCRUD struct {
	controllers map[string]*coreapi.Controller
}

func newFakeControllerCRUD() *fakeControllerCRUD {
	return &fakeControllerCRUD{
		controllers: make(map[string]*coreapi.Controller),
	}
}

func (f *fakeControllerCRUD) GetByID(ctx context.Context, cosmosID string) (*coreapi.Controller, error) {
	return nil, nil
}

func (f *fakeControllerCRUD) Get(ctx context.Context, resourceID string) (*coreapi.Controller, error) {
	if c, ok := f.controllers[resourceID]; ok {
		return c, nil
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (f *fakeControllerCRUD) List(ctx context.Context, opts *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Controller], error) {
	return nil, nil
}

func (f *fakeControllerCRUD) Create(ctx context.Context, newObj *coreapi.Controller, options *azcosmos.ItemOptions) (*coreapi.Controller, error) {
	f.controllers[newObj.ResourceID.Name] = newObj
	return newObj, nil
}

func (f *fakeControllerCRUD) Replace(ctx context.Context, newObj *coreapi.Controller, options *azcosmos.ItemOptions) (*coreapi.Controller, error) {
	f.controllers[newObj.ResourceID.Name] = newObj
	return newObj, nil
}

func (f *fakeControllerCRUD) Delete(ctx context.Context, resourceID string) error {
	delete(f.controllers, resourceID)
	return nil
}

func (f *fakeControllerCRUD) DeleteWithETag(context.Context, string, azcore.ETag) error {
	panic("not implemented")
}

func (f *fakeControllerCRUD) AddCreateToTransaction(ctx context.Context, transaction cosmosstorageutils.DBTransaction, newObj *coreapi.Controller, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "", nil
}

func (f *fakeControllerCRUD) AddReplaceToTransaction(ctx context.Context, transaction cosmosstorageutils.DBTransaction, newObj *coreapi.Controller, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "", nil
}

func TestDegradedControllerPanicHandler(t *testing.T) {
	controllerName := "test-controller"
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"

	initialController := func(name string) *coreapi.Controller {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroup +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
				"/" + coreapi.ControllerResourceTypeName + "/" + name))
		clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroup +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))
		return &coreapi.Controller{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			ExternalID: clusterResourceID,
			Status: coreapi.ControllerStatus{
				Conditions: []metav1.Condition{},
			},
		}
	}

	t.Run("captures panic value and stack trace", func(t *testing.T) {
		fakeCRUD := newFakeControllerCRUD()
		ctx := context.Background()

		handler := DegradedControllerPanicHandler(ctx, fakeCRUD, controllerName, initialController)

		// Call the handler with a panic value
		handler("test panic message")

		// Verify the controller was created with the Degraded condition
		controller := fakeCRUD.controllers[controllerName]
		require.NotNil(t, controller, "controller should have been created")

		degradedCondition := meta.FindStatusCondition(controller.Status.Conditions, "Degraded")
		require.NotNil(t, degradedCondition, "Degraded condition should exist")
		require.Equal(t, metav1.ConditionTrue, degradedCondition.Status, "Degraded condition should be True")
		require.Equal(t, "Failed", degradedCondition.Reason, "Degraded condition reason should be Failed")

		// Verify the message contains the panic value
		require.Contains(t, degradedCondition.Message, "test panic message", "message should contain panic value")

		// Verify the message contains stack trace indicators
		require.Contains(t, degradedCondition.Message, "panic caught:", "message should indicate panic was caught")
		require.Contains(t, degradedCondition.Message, "goroutine", "message should contain stack trace with goroutine info")
		require.Contains(t, degradedCondition.Message, ".go:", "message should contain stack trace with file references")
	})

	t.Run("captures error type panic with stack trace", func(t *testing.T) {
		fakeCRUD := newFakeControllerCRUD()
		ctx := context.Background()

		handler := DegradedControllerPanicHandler(ctx, fakeCRUD, controllerName, initialController)

		// Call the handler with an error panic value
		handler(strings.NewReader("error-like panic"))

		controller := fakeCRUD.controllers[controllerName]
		require.NotNil(t, controller, "controller should have been created")

		degradedCondition := meta.FindStatusCondition(controller.Status.Conditions, "Degraded")
		require.NotNil(t, degradedCondition, "Degraded condition should exist")

		// Verify stack trace is present even for non-string panic values
		require.Contains(t, degradedCondition.Message, "goroutine", "message should contain stack trace")
	})
}

func TestRedactDynamicSyncErrorMessageFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "AADSTS inline and JSON fields are redacted",
			input: `ClientCertificateCredential authentication failed.
POST https://example.com/YYYYYYY/oauth2/v2.0/token
{
  "error": "invalid_grant",
  "error_description": "AADSTS5000229: The tenant '<removed>' is not available due to expiration. Trace ID: aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee Correlation ID: 11111111-2222-3333-4444-555555555555 Timestamp: 2026-11-02 15:49:40Z",
  "timestamp": "2026-11-02 15:49:40Z",
  "trace_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  "correlation_id": "11111111-2222-3333-4444-555555555555"
}`,
			want: `ClientCertificateCredential authentication failed.
POST https://example.com/YYYYYYY/oauth2/v2.0/token
{
  "error": "invalid_grant",
  "error_description": "AADSTS5000229: The tenant '<removed>' is not available due to expiration. Trace ID: <removed> Correlation ID: <removed> Timestamp: <removed>",
  "timestamp": "<removed>",
  "trace_id": "<removed>",
  "correlation_id": "<removed>"
}`,
		},
		{
			name:  "AADSTS tenant expiration with GUID is redacted",
			input: `error_description: "AADSTS5000229: The tenant 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee' is not available due to expiration. To learn more about tenant expiration policies, see https://aka.ms/tenantExpiryPolicy"`,
			want:  `error_description: "AADSTS5000229: The tenant '<removed>' is not available due to expiration. To learn more about tenant expiration policies, see https://aka.ms/tenantExpiryPolicy"`,
		},
		{
			name:  "AADSTS tenant expiration with display name is redacted",
			input: `error_description: "AADSTS5000229: The tenant 'my-test-tenant' is not available due to expiration. Trace ID: aaaa-bbbb Correlation ID: cccc-dddd Timestamp: 2026-11-04 14:41:07Z"`,
			want:  `error_description: "AADSTS5000229: The tenant '<removed>' is not available due to expiration. Trace ID: <removed> Correlation ID: <removed> Timestamp: <removed>"`,
		},
		{
			name:  "AADSTS inline timestamp stops before trailing text",
			input: `error_description: "... Timestamp: 2026-11-02 15:49:40Z. Please retry the request or contact support."`,
			want:  `error_description: "... Timestamp: <removed>. Please retry the request or contact support."`,
		},
		{
			name:  "AADSTS inline timestamp handles ISO8601 with fractional seconds",
			input: `error_description: "... Timestamp: 2026-11-02T15:49:40.1234567Z Additional context here."`,
			want:  `error_description: "... Timestamp: <removed> Additional context here."`,
		},
		{
			name:  "AADSTS inline timestamp handles explicit UTC offset",
			input: `error_description: "... Timestamp: 2026-11-02 15:49:40+00:00 trailing note"`,
			want:  `error_description: "... Timestamp: <removed> trailing note"`,
		},
		{
			name:  "AADSTS inline Trace ID and Correlation ID stop at punctuation",
			input: `error_description: "... Trace ID: aaaa-bbbb, Correlation ID: cccc-dddd."`,
			want:  `error_description: "... Trace ID: <removed>, Correlation ID: <removed>."`,
		},
		{
			name:  "AADSTS inline Trace ID stops before a closing parenthesis, semicolon, question mark, or newline",
			input: "error_description: \"(see Trace ID: aaaa-bbbb); Correlation ID: cccc-dddd? Timestamp: 2026-11-02 15:49:40Z\nmore text\"",
			want:  "error_description: \"(see Trace ID: <removed>); Correlation ID: <removed>? Timestamp: <removed>\nmore text\"",
		},
		{
			name:  "AADSTS near-miss text without a colon is not matched",
			input: "error_description: \"Trace IDs and Correlation IDs are useful for debugging.\"",
			want:  "error_description: \"Trace IDs and Correlation IDs are useful for debugging.\"",
		},
		{
			name:  "AADSTS multiple occurrences of the same field are all redacted",
			input: `error_description: "Trace ID: aaaa-bbbb at step one. Trace ID: cccc-dddd at step two."`,
			want:  `error_description: "Trace ID: <removed> at step one. Trace ID: <removed> at step two."`,
		},
		{
			name:  "ARM retry-after handles a decimal wait value",
			input: `"message": "... Retry after 2.5 seconds"`,
			want:  `"message": "... Retry after <removed> seconds"`,
		},
		{
			name:  "ARM retry-after redacts only the number and keeps the unit",
			input: `"message": "Request is temporarily throttled because subscription XXXXX has issued too many requests. Retry after 3 seconds."`,
			want:  `"message": "Request is temporarily throttled because subscription XXXXX has issued too many requests. Retry after <removed> seconds."`,
		},
		{
			name:  "ARM retry-after preserves a non-seconds unit instead of claiming seconds",
			input: `"message": "... Retry after 60 minutes due to sustained load"`,
			want:  `"message": "... Retry after <removed> minutes due to sustained load"`,
		},
		{
			name:  "ARM retry-after leaves trailing text after the unit untouched",
			input: `"message": "... Retry after 3 seconds, then retry the operation"`,
			want:  `"message": "... Retry after <removed> seconds, then retry the operation"`,
		},
		{
			name:  "ARM subscription throttled redacts the quoted try-again-after wait value",
			input: `"retrievalError": "GET https://example.com/subscriptions/XXXXX/resourceGroups/XXXX/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-controller-manager\n--------------------------------------------------------------------------------\nRESPONSE 429: 429 Too Many Requests\nERROR CODE: SubscriptionRequestsThrottled\n--------------------------------------------------------------------------------\n{\n  \"error\": {\n    \"code\": \"SubscriptionRequestsThrottled\",\n    \"message\": \"Number of 'read' requests for subscription 'XXXX' actor 'XXXXX' exceeded. Please try again after '10' seconds after additional tokens are available. Refer to https://aka.ms/arm-throttling for additional information.\"\n  }\n}\n--------------------------------------------------------------------------------\n"`,
			want:  `"retrievalError": "GET https://example.com/subscriptions/XXXXX/resourceGroups/XXXX/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-controller-manager\n--------------------------------------------------------------------------------\nRESPONSE 429: 429 Too Many Requests\nERROR CODE: SubscriptionRequestsThrottled\n--------------------------------------------------------------------------------\n{\n  \"error\": {\n    \"code\": \"SubscriptionRequestsThrottled\",\n    \"message\": \"Number of 'read' requests for subscription 'XXXX' actor 'XXXXX' exceeded. Please try again after '<removed>' seconds after additional tokens are available. Refer to https://aka.ms/arm-throttling for additional information.\"\n  }\n}\n--------------------------------------------------------------------------------\n"`,
		},
		{
			name:  "ARM subscription throttled preserves a non-seconds unit instead of claiming seconds",
			input: `"message": "Number of 'read' requests exceeded. Please try again after '500' milliseconds after additional tokens are available."`,
			want:  `"message": "Number of 'read' requests exceeded. Please try again after '<removed>' milliseconds after additional tokens are available."`,
		},
		{
			name:  "Cluster Service timestamp and operation identifier are redacted, other quoted values are not",
			input: `ClusterClusterServiceCreate: Had an error while syncing: (wrapped at cluster_cluster_service_create_controller.go:193) failed to create cluster in CS: (wrapped at cluster_cluster_service_create_controller.go:341) PostCluster failed: (wrapped at client.go:399) status is 400, identifier is '400', code is 'CLUSTERS-MGMT-400', at '2026-10-22T16:59:33Z' and operation identifier is '98604e1b-af8d-470b-9ccc-f21eb226a3cf': Version 'openshift-v5.0.0-0.nightly-multi-2026-08-28-044726-nightly' doesn't exist .`,
			want:  `ClusterClusterServiceCreate: Had an error while syncing: (wrapped at cluster_cluster_service_create_controller.go:193) failed to create cluster in CS: (wrapped at cluster_cluster_service_create_controller.go:341) PostCluster failed: (wrapped at client.go:399) status is 400, identifier is '400', code is 'CLUSTERS-MGMT-400', at '<removed>' and operation identifier is '<removed>': Version 'openshift-v5.0.0-0.nightly-multi-2026-08-28-044726-nightly' doesn't exist .`,
		},
		{
			name:  "message with no dynamic fields is returned unchanged",
			input: "failed to create cluster: some static, non-matching error",
			want:  "failed to create cluster: some static, non-matching error",
		},
		{
			name:  "empty message is returned unchanged",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, RedactDynamicSyncErrorMessageFields(tt.input))
		})
	}
}

func TestRedactDynamicErrorFieldsNormalizesRepeatedFailures(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
	}{
		{
			name: "AADSTS error with different trace/correlation IDs and timestamps normalizes to the same message",
			a:    `error_description: "AADSTS5000229: tenant expired. Trace ID: aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee Correlation ID: 11111111-2222-3333-4444-555555555555 Timestamp: 2026-11-02 15:49:40Z"`,
			b:    `error_description: "AADSTS5000229: tenant expired. Trace ID: ffffffff-0000-1111-2222-333333333333 Correlation ID: 99999999-8888-7777-6666-555555555555 Timestamp: 2026-11-03 09:01:02Z"`,
		},
		{
			name: "ARM throttling error with different retry durations normalizes to the same message",
			a:    `"message": "Request is temporarily throttled because subscription XXXXX has issued too many requests. Retry after 3 seconds."`,
			b:    `"message": "Request is temporarily throttled because subscription XXXXX has issued too many requests. Retry after 17 seconds."`,
		},
		{
			name: "Cluster Service error with different timestamps and operation identifiers normalizes to the same message",
			a:    `PostCluster failed: status is 400, code is 'CLUSTERS-MGMT-400', at '2026-10-22T16:59:33Z' and operation identifier is '98604e1b-af8d-470b-9ccc-f21eb226a3cf': Version 'x' doesn't exist .`,
			b:    `PostCluster failed: status is 400, code is 'CLUSTERS-MGMT-400', at '2026-10-23T09:12:01Z' and operation identifier is 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee': Version 'x' doesn't exist .`,
		},
		{
			name: "AADSTS tenant expiration error with different tenant identifiers (GUID vs display name) normalizes to the same message",
			a:    `error_description: "AADSTS5000229: The tenant 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee' is not available due to expiration. Trace ID: aaaa-bbbb Correlation ID: cccc-dddd Timestamp: 2026-11-04 14:41:07Z"`,
			b:    `error_description: "AADSTS5000229: The tenant 'my-test-tenant-name' is not available due to expiration. Trace ID: ffff-eeee Correlation ID: 1111-2222 Timestamp: 2026-11-05 09:12:34Z"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, RedactDynamicSyncErrorMessageFields(tt.a), RedactDynamicSyncErrorMessageFields(tt.b))
		})
	}
}
