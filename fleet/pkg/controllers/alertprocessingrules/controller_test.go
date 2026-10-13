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

package alertprocessingrules

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000001"
	testResourceGroup  = "rg-alert-processing-rules"
)

// fakeCredential satisfies the bearer token policy without contacting Entra.
type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// fakeTransport records every request the SDK issues and serves canned responses.
type fakeTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	handler  func(req *http.Request) *http.Response
}

func (t *fakeTransport) Do(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.requests = append(t.requests, req)
	t.mu.Unlock()

	resp := t.handler(req)
	// azcore reads resp.Request when turning a non-success status into a
	// *azcore.ResponseError.
	if resp.Request == nil {
		resp.Request = req
	}
	if resp.Body == nil {
		resp.Body = io.NopCloser(strings.NewReader(""))
	}
	return resp, nil
}

// deleted returns the rule names targeted by DELETE requests, in order.
func (t *fakeTransport) deleted() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var names []string
	for _, req := range t.requests {
		if req.Method == http.MethodDelete {
			names = append(names, path.Base(req.URL.Path))
		}
	}
	return names
}

func jsonResponse(t *testing.T, status int, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err, "failed to encode canned response body")
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(encoded))),
	}
}

func errorResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"Failed","message":"synthetic failure"}}`)),
	}
}

func newTestController(t *testing.T, transport *fakeTransport, expiryThreshold time.Duration) *Controller {
	t.Helper()

	client, err := armalertprocessingrules.NewClient(testSubscriptionID, fakeCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: transport,
			// Negative disables retries, so a synthetic 5xx fails the pass immediately
			// instead of stalling the test behind azcore's backoff.
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	})
	require.NoError(t, err, "failed to build the alert processing rules client")

	return &Controller{
		pollInterval:    time.Minute,
		expiryThreshold: expiryThreshold,
		resourceGroup:   testResourceGroup,
		subscriptionID:  testSubscriptionID,
		client:          client,
		now:             func() time.Time { return now },
	}
}

// TestReconcileReapsOnlyEligibleRules is the core acceptance test: a resource group
// holding a mix of rules loses exactly the expired, tagged, non-recurring ones.
func TestReconcileReapsOnlyEligibleRules(t *testing.T) {
	rules := []*armalertprocessingrules.AlertProcessingRule{
		namedRule("expired-tagged-oneshot"),
		namedRule("expired-untagged", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Tags = nil
		}),
		namedRule("expired-other-purpose", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Tags = map[string]*string{PurposeTagKey: ptr.To("service")}
		}),
		namedRule("expired-recurring", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Properties.Schedule.Recurrences = []armalertprocessingrules.RecurrenceClassification{
				&armalertprocessingrules.DailyRecurrence{
					RecurrenceType: ptr.To(armalertprocessingrules.RecurrenceTypeDaily),
				},
			}
		}),
		namedRule("not-yet-expired", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Properties.Schedule.EffectiveUntil = ptr.To("2026-10-07T12:00:00")
		}),
		namedRule("within-grace-period", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Properties.Schedule.EffectiveUntil = ptr.To("2026-10-06T11:30:00")
		}),
		namedRule("permanent-no-schedule", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Properties.Schedule = nil
		}),
		namedRule("second-expired-tagged-oneshot"),
	}

	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		if req.Method == http.MethodDelete {
			return &http.Response{StatusCode: http.StatusOK}
		}
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{Value: rules})
	}

	c := newTestController(t, transport, testExpiryThreshold)
	c.reconcile(context.Background(), logr.Discard())

	assert.ElementsMatch(t,
		[]string{"expired-tagged-oneshot", "second-expired-tagged-oneshot"},
		transport.deleted(),
		"only expired, tagged, non-recurring rules should be deleted",
	)
}

// TestReconcileLeavesEverythingAloneWhenNothingIsEligible guards the "untouched"
// half of the acceptance criteria independently of the reaping path.
func TestReconcileLeavesEverythingAloneWhenNothingIsEligible(t *testing.T) {
	rules := []*armalertprocessingrules.AlertProcessingRule{
		namedRule("untagged", func(r *armalertprocessingrules.AlertProcessingRule) { r.Tags = nil }),
		namedRule("future", func(r *armalertprocessingrules.AlertProcessingRule) {
			r.Properties.Schedule.EffectiveUntil = ptr.To("2027-01-01T00:00:00")
		}),
	}

	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{Value: rules})
	}

	c := newTestController(t, transport, testExpiryThreshold)
	c.reconcile(context.Background(), logr.Discard())

	assert.Empty(t, transport.deleted(), "no rule met all three criteria, so none should be deleted")
}

// TestReconcileContinuesAfterDeleteFailure confirms one bad rule does not strand the
// rest of the resource group.
func TestReconcileContinuesAfterDeleteFailure(t *testing.T) {
	rules := []*armalertprocessingrules.AlertProcessingRule{
		namedRule("fails-to-delete"),
		namedRule("deletes-fine"),
	}

	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		if req.Method == http.MethodDelete {
			if path.Base(req.URL.Path) == "fails-to-delete" {
				return errorResponse(http.StatusInternalServerError)
			}
			return &http.Response{StatusCode: http.StatusNoContent}
		}
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{Value: rules})
	}

	c := newTestController(t, transport, testExpiryThreshold)
	c.reconcile(context.Background(), logr.Discard())

	assert.ElementsMatch(t, []string{"fails-to-delete", "deletes-fine"}, transport.deleted(),
		"a failure on one rule must not skip the remaining rules")
}

// TestReconcileDoesNotDeleteWhenListFails ensures a failed list is not mistaken for
// an empty resource group.
func TestReconcileDoesNotDeleteWhenListFails(t *testing.T) {
	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		return errorResponse(http.StatusInternalServerError)
	}

	c := newTestController(t, transport, testExpiryThreshold)
	c.reconcile(context.Background(), logr.Discard())

	assert.Empty(t, transport.deleted(), "a failed list must not trigger any deletion")
}

// TestReconcileStopsOnCancelledContext covers context-aware shutdown mid-pass.
func TestReconcileStopsOnCancelledContext(t *testing.T) {
	rules := []*armalertprocessingrules.AlertProcessingRule{
		namedRule("rule-a"), namedRule("rule-b"), namedRule("rule-c"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		if req.Method == http.MethodDelete {
			// Cancel after the first delete; the pass should abandon the rest.
			cancel()
			return &http.Response{StatusCode: http.StatusOK}
		}
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{Value: rules})
	}
	defer cancel()

	c := newTestController(t, transport, testExpiryThreshold)
	c.reconcile(ctx, logr.Discard())

	assert.Len(t, transport.deleted(), 1, "reconcile should abandon the pass once the context is cancelled")
}

// TestDeleteRuleTreatsNotFoundAsSuccess covers the concurrent-operator-action race:
// the rule vanished between our list and our delete.
func TestDeleteRuleTreatsNotFoundAsSuccess(t *testing.T) {
	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		return errorResponse(http.StatusNotFound)
	}

	c := newTestController(t, transport, testExpiryThreshold)
	err := c.deleteRule(context.Background(), logr.Discard(), "already-gone")

	assert.NoError(t, err, "a rule deleted by someone else is not a failure")
}

func TestDeleteRuleAcceptsNoContent(t *testing.T) {
	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusNoContent}
	}

	c := newTestController(t, transport, testExpiryThreshold)

	assert.NoError(t, c.deleteRule(context.Background(), logr.Discard(), "gone-now"),
		"ARM returns 204 when deleting an alert processing rule")
}

func TestDeleteRulePropagatesOtherErrors(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusConflict} {
		transport := &fakeTransport{}
		transport.handler = func(req *http.Request) *http.Response {
			return errorResponse(status)
		}

		c := newTestController(t, transport, testExpiryThreshold)
		err := c.deleteRule(context.Background(), logr.Discard(), "some-rule")

		assert.Error(t, err, "HTTP %d should surface as a delete failure", status)
	}
}

// TestListFollowsPagination guards against reaping only the first page of a resource
// group that is near the 1000-rule cap.
func TestListFollowsPagination(t *testing.T) {
	const nextLink = "https://management.azure.com/next-page"

	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		if strings.Contains(req.URL.Path, "next-page") {
			return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{
				Value: []*armalertprocessingrules.AlertProcessingRule{namedRule("page-two-rule")},
			})
		}
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{
			Value:    []*armalertprocessingrules.AlertProcessingRule{namedRule("page-one-rule")},
			NextLink: ptr.To(nextLink),
		})
	}

	c := newTestController(t, transport, testExpiryThreshold)
	rules, err := c.list(context.Background())
	require.NoError(t, err, "listing across pages should succeed")

	require.Len(t, rules, 2, "both pages should be collected")
	assert.Equal(t, "page-one-rule", *rules[0].Name, "first page rule missing")
	assert.Equal(t, "page-two-rule", *rules[1].Name, "second page rule missing")
}

// TestListTargetsConfiguredResourceGroup pins the request the SDK builds, so the
// controller cannot silently sweep the wrong scope.
func TestListTargetsConfiguredResourceGroup(t *testing.T) {
	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{})
	}

	c := newTestController(t, transport, testExpiryThreshold)
	_, err := c.list(context.Background())
	require.NoError(t, err, "listing an empty resource group should succeed")

	require.Len(t, transport.requests, 1, "expected exactly one list request")
	assert.Equal(t,
		"/subscriptions/"+testSubscriptionID+"/resourceGroups/"+testResourceGroup+
			"/providers/Microsoft.AlertsManagement/actionRules",
		transport.requests[0].URL.Path,
		"list must be scoped to the configured regional resource group",
	)
}

func TestNewControllerParsesResourceGroupID(t *testing.T) {
	c, err := NewController(
		DefaultPollInterval,
		DefaultExpiryThreshold,
		"/subscriptions/"+testSubscriptionID+"/resourceGroups/"+testResourceGroup,
		fakeCredential{},
		&policy.ClientOptions{},
	)
	require.NoError(t, err, "a well-formed resource group ID should be accepted")

	assert.Equal(t, testSubscriptionID, c.subscriptionID, "subscription should come from the resource group ID")
	assert.Equal(t, testResourceGroup, c.resourceGroup, "resource group should come from the resource group ID")
	assert.NotNil(t, c.client, "a configured controller should have an Azure client")
}

func TestNewControllerRejectsMalformedResourceGroupID(t *testing.T) {
	_, err := NewController(
		DefaultPollInterval, DefaultExpiryThreshold,
		"not-a-resource-id", fakeCredential{}, &policy.ClientOptions{},
	)
	assert.Error(t, err, "a malformed resource group ID should be rejected at construction")
}

// TestRunIsNoOpWhenUnconfigured confirms the controller stays dormant (and never
// dereferences a nil client) in environments that have not opted in.
func TestRunIsNoOpWhenUnconfigured(t *testing.T) {
	c, err := NewController(DefaultPollInterval, DefaultExpiryThreshold, "", nil, nil)
	require.NoError(t, err, "an empty resource group ID should disable rather than fail")
	require.Nil(t, c.client, "an unconfigured controller should not build an Azure client")

	done := make(chan struct{})
	go func() {
		defer close(done)
		// A context that is never cancelled: Run must return on its own.
		c.Run(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run should return immediately when no resource group is configured")
	}
}

// TestRunStopsOnContextCancellation covers context-aware shutdown of the poll loop.
func TestRunStopsOnContextCancellation(t *testing.T) {
	transport := &fakeTransport{}
	transport.handler = func(req *http.Request) *http.Response {
		return jsonResponse(t, http.StatusOK, armalertprocessingrules.List{})
	}

	c := newTestController(t, transport, testExpiryThreshold)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run should return once its context is cancelled")
	}
}

// namedRule builds an eligible rule under a given name, before options are applied.
func namedRule(name string, options ...ruleOption) *armalertprocessingrules.AlertProcessingRule {
	rule := newRule(options...)
	rule.Name = ptr.To(name)
	return rule
}
