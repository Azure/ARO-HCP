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

// Package alertprocessingrules implements a controller that reaps expired alert
// processing rules (Microsoft.AlertsManagement/actionRules) from a region's alert
// processing rule resource group.
//
// ARO HCP creates alert processing rules to temporarily suppress alert notifications.
// Those rules are one-shot: they carry an effectiveUntil and no recurrence, and Azure
// stops applying them once that deadline passes — but it does not delete them. Azure
// caps a subscription at 1000 alert processing rules by default, so without a reaper
// the expired rules eventually exhaust the quota and new suppressions start failing.
//
// On each pass the controller lists the rules in the configured resource group and
// deletes every rule that:
//
//  1. carries the aroHCPPurpose=alert-processing-rule tag,
//  2. has no recurrence, and
//  3. has an effectiveUntil older than the configured expiry threshold.
//
// Rules failing any criterion are left untouched; see evaluate in eligibility.go for
// how ambiguous rules are handled. Deletion is idempotent — a rule already removed by
// a concurrent operator action returns 404 and is counted as reaped.
//
// The controller follows the poll-loop shape of the amwscaling controller: it runs
// under the fleet controller-manager's leader election, so exactly one replica per
// region reaps at a time.
package alertprocessingrules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// controllerName is the well-known name for this controller, used in logging and metrics.
	controllerName = "AlertProcessingRuleReaping"

	// DefaultPollInterval is how often the resource group is swept. The acceptance
	// criterion is that an expired rule is removed within five minutes, so this leaves
	// headroom for the list and delete round-trips within that budget.
	DefaultPollInterval = 2 * time.Minute

	// DefaultExpiryThreshold is how far past its effectiveUntil a rule must be before
	// it is reaped. This is a safety margin, not a quota control: a rule is already
	// inert once effectiveUntil passes, so waiting an hour costs nothing but protects
	// against clock skew between us and the Azure control plane, and leaves a window
	// in which an operator can inspect a just-expired suppression.
	DefaultExpiryThreshold = time.Hour
)

// RulesClient is the subset of armalertprocessingrules.Client the reaper needs.
//
// Taking the generated client through an interface keeps request construction in the
// SDK (rather than hand-rolled REST calls) while letting tests substitute a fake and
// letting the shared client from the Admin API CRUD work drop in unchanged.
type RulesClient interface {
	NewListByResourceGroupPager(
		resourceGroupName string,
		options *armalertprocessingrules.ClientListByResourceGroupOptions,
	) *runtime.Pager[armalertprocessingrules.ClientListByResourceGroupResponse]

	Delete(
		ctx context.Context,
		resourceGroupName string,
		alertProcessingRuleName string,
		options *armalertprocessingrules.ClientDeleteOptions,
	) (armalertprocessingrules.ClientDeleteResponse, error)
}

// Assert the generated SDK client satisfies the interface we consume it through.
var _ RulesClient = (*armalertprocessingrules.Client)(nil)

// Controller periodically deletes expired alert processing rules from a single
// resource group.
type Controller struct {
	pollInterval    time.Duration
	expiryThreshold time.Duration
	resourceGroup   string
	subscriptionID  string
	client          RulesClient

	// now is overridable so tests can pin the clock against fixed effectiveUntil values.
	now func() time.Time
}

// NewController creates a new alert processing rule reaping controller.
//
// resourceGroupResourceID is the ARM resource ID of the region's alert processing rule
// resource group (/subscriptions/{sub}/resourceGroups/{rg}); the subscription the rules
// live in is taken from it. Rules must live in the same subscription as the Azure Monitor
// Workspace they scope, so this is deliberately a regional resource group rather than a
// global one, which may span subscriptions.
//
// An empty resourceGroupResourceID disables the controller, which then logs and returns
// from Run without doing any work.
func NewController(
	pollInterval time.Duration,
	expiryThreshold time.Duration,
	resourceGroupResourceID string,
	credential azcore.TokenCredential,
	clientOptions *policy.ClientOptions,
) (*Controller, error) {
	c := &Controller{
		pollInterval:    pollInterval,
		expiryThreshold: expiryThreshold,
		now:             time.Now,
	}

	// When no resource group is configured the controller is a no-op, so we skip
	// building the Azure client, which requires a non-nil credential.
	if len(resourceGroupResourceID) == 0 {
		return c, nil
	}

	parsed, err := azcorearm.ParseResourceID(resourceGroupResourceID)
	if err != nil {
		return nil, fmt.Errorf("parsing alert processing rule resource group ID %q: %w", resourceGroupResourceID, err)
	}
	c.subscriptionID = parsed.SubscriptionID
	c.resourceGroup = parsed.ResourceGroupName

	if clientOptions == nil {
		clientOptions = &policy.ClientOptions{}
	}
	client, err := armalertprocessingrules.NewClient(c.subscriptionID, credential, &azcorearm.ClientOptions{
		ClientOptions: *clientOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("creating alert processing rules client: %w", err)
	}
	c.client = client

	return c, nil
}

// Run starts the controller loop. It blocks until the context is cancelled.
func (c *Controller) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	ctx = utils.ContextWithControllerName(ctx, controllerName)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(controllerName)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	if c.client == nil {
		logger.Info("No alert processing rule resource group configured, controller will not run")
		return
	}

	logger = logger.WithValues(
		"subscriptionID", c.subscriptionID,
		"resourceGroup", c.resourceGroup,
	)

	logger.Info("Starting",
		"pollInterval", c.pollInterval,
		"expiryThreshold", c.expiryThreshold,
	)

	// Reap immediately on start, then on ticker.
	c.reconcile(ctx, logger)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopped")
			return
		case <-ticker.C:
			c.reconcile(ctx, logger)
		}
	}
}

// reconcile performs a single reaping pass. Errors are logged and counted rather than
// returned: a pass is best-effort, and a failure on one rule must not prevent the
// remaining rules from being reclaimed.
func (c *Controller) reconcile(ctx context.Context, logger logr.Logger) {
	base.ReconcileTotal.WithLabelValues(controllerName).Inc()

	rules, err := c.list(ctx)
	if err != nil {
		listErrorsTotal.Inc()
		logger.Error(err, "Failed to list alert processing rules")
		return
	}
	observedRules.Set(float64(len(rules)))

	now := c.now()
	var expired, deleted, failed int

	for _, rule := range rules {
		// Stop promptly on shutdown rather than working through a long list.
		if ctx.Err() != nil {
			logger.Info("Reaping pass interrupted by shutdown",
				"observed", len(rules), "expired", expired, "deleted", deleted, "failed", failed)
			return
		}

		d := evaluate(rule, now, c.expiryThreshold)
		if !d.reap {
			logger.V(4).Info("Leaving alert processing rule in place",
				"rule", ruleName(rule), "reason", d.reason)
			continue
		}
		expired++

		ruleLogger := logger.WithValues("rule", *rule.Name, "reason", d.reason)
		if err := c.deleteRule(ctx, ruleLogger, *rule.Name); err != nil {
			failed++
			deleteErrorsTotal.Inc()
			ruleLogger.Error(err, "Failed to delete expired alert processing rule")
			continue
		}
		deleted++
		deletedRulesTotal.Inc()
	}

	expiredRules.Set(float64(expired))

	logger.Info("Completed alert processing rule reaping pass",
		"observed", len(rules), "expired", expired, "deleted", deleted, "failed", failed)
}

// list returns every alert processing rule in the configured resource group.
func (c *Controller) list(ctx context.Context) ([]*armalertprocessingrules.AlertProcessingRule, error) {
	pager := c.client.NewListByResourceGroupPager(c.resourceGroup, nil)

	var rules []*armalertprocessingrules.AlertProcessingRule
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing alert processing rules in resource group %q: %w", c.resourceGroup, err)
		}
		rules = append(rules, page.Value...)
	}
	return rules, nil
}

// deleteRule deletes a single alert processing rule, treating an already-deleted rule
// as success. A rule can disappear between our list and our delete if an operator
// cleaned it up by hand, and that race is a no-op, not a failure.
func (c *Controller) deleteRule(ctx context.Context, logger logr.Logger, name string) error {
	if _, err := c.client.Delete(ctx, c.resourceGroup, name, nil); err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			logger.Info("Expired alert processing rule was already deleted")
			return nil
		}
		return fmt.Errorf("deleting alert processing rule %q: %w", name, err)
	}

	logger.Info("Deleted expired alert processing rule")
	return nil
}

// ruleName renders a rule's name for logging, tolerating the unnamed rules that
// evaluate skips.
func ruleName(rule *armalertprocessingrules.AlertProcessingRule) string {
	if rule == nil || rule.Name == nil {
		return "<unnamed>"
	}
	return *rule.Name
}
