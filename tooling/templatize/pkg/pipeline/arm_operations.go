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

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// Operation describes an ARM deployment rollout operation taken to realize a template.
type Operation struct {
	// OperationType is what the operation did - known values: "Create", "Read", "EvaluateDeploymentOutput"
	OperationType string `json:"operationType"`

	// StartTimestamp is the time at which the operation started, formatted as RFC3339 date+time: 2025-11-05T13:16:20.624264+00:00
	StartTimestamp string `json:"startTimestamp"`
	// Duration is the time taken to run the operation, formatted as RFC3339 duration: PT3M12.9884364S
	Duration string `json:"duration"`

	// ProvisioningState is the terminal (or current) state of the operation, like "Succeeded" or "Failed".
	ProvisioningState string `json:"provisioningState,omitempty"`
	// StatusCode is the HTTP status code the resource provider returned for the operation, like "OK" or "Conflict".
	StatusCode string `json:"statusCode,omitempty"`
	// Error holds the error reported for the operation, if any. Subscription IDs are redacted.
	Error *OperationError `json:"error,omitempty"`

	// Resource defines the object of this operation.
	Resource *Resource `json:"resource,omitempty"`

	// Children holds the child operations when the resource is another deployment.
	Children []Operation `json:"children,omitempty"`
	// ChildrenIncomplete is set when the resource is another deployment whose own operations could not all be fetched,
	// so Children may be missing some.
	ChildrenIncomplete bool `json:"childrenIncomplete,omitempty"`
}

// OperationError is the error reported by ARM for a deployment operation.
type OperationError struct {
	Code    string           `json:"code,omitempty"`
	Message string           `json:"message,omitempty"`
	Target  string           `json:"target,omitempty"`
	Details []OperationError `json:"details,omitempty"`
}

type Resource struct {
	// ResourceType is the resource provider and resource name, like "Microsoft.KeyVault/vaults".
	ResourceType string `json:"resourceType"`
	// SubscriptionID is the Azure subscription in which the resource exists.
	// Not serialized to avoid leaking sensitive identifiers into published artifacts.
	SubscriptionID string `json:"-"`
	// ResourceGroup is the Azure resource group in which the resource exists.
	ResourceGroup string `json:"resourceGroup"`
	// Name is the name of the resource.
	Name string `json:"name"`
}

// OperationsClientGetter returns a DeploymentOperationsClient for the given subscription ID.
// Implementations should cache clients to avoid redundant authentication.
type OperationsClientGetter func(subscriptionID string) (*armresources.DeploymentOperationsClient, error)

// NewCachedOperationsClientGetter creates an OperationsClientGetter that caches clients per subscription.
// The provided defaultClient is used for the given subscriptionID; clients for other subscriptions
// are created lazily using the provided credential.
func NewCachedOperationsClientGetter(subscriptionID string, defaultClient *armresources.DeploymentOperationsClient, cred azcore.TokenCredential, opts *azcorearm.ClientOptions) OperationsClientGetter {
	// mu guards the clients map, which may be accessed concurrently if
	// multiple deployment operations are fetched in parallel.
	var mu sync.Mutex
	clients := map[string]*armresources.DeploymentOperationsClient{
		subscriptionID: defaultClient,
	}
	return func(subID string) (*armresources.DeploymentOperationsClient, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := clients[subID]; ok {
			return c, nil
		}
		c, err := armresources.NewDeploymentOperationsClient(subID, cred, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to create operations client for subscription: %w", err)
		}
		clients[subID] = c
		return c, nil
	}
}

func DetermineOperationsForResourceGroupDeployment(getClient OperationsClientGetter, subscriptionID, resourceGroup, deploymentName string) DetailsProducer {
	return func(ctx context.Context) (*ExecutionDetails, error) {
		ops, err := fetchOperationsFor(ctx, getClient, subscriptionID, resourceGroup, deploymentName)
		return executionDetailsFor(ops, err), err
	}
}

func DetermineOperationsForSubscriptionDeployment(getClient OperationsClientGetter, subscriptionID, deploymentName string) DetailsProducer {
	return func(ctx context.Context) (*ExecutionDetails, error) {
		ops, err := fetchSubscriptionScopedOperationsFor(ctx, getClient, subscriptionID, deploymentName)
		return executionDetailsFor(ops, err), err
	}
}

// executionDetailsFor wraps possibly-partial results; nil when fetching failed before anything was found.
func executionDetailsFor(ops []Operation, err error) *ExecutionDetails {
	if err != nil && len(ops) == 0 {
		return nil
	}
	return &ExecutionDetails{ARM: &ARMExecutionDetails{Operations: ops}}
}

// n.b. the Azure SDK for Go has unique types for the different types of pagers and no type constraint can be written to scope for pages of things containing lists of ops, so just copy-paste this instead of being clever

// fetchOperationsFor retrieves ARM deployment operations, handling both resource-group-scoped
// and subscription-scoped deployments. Recursively fetches child deployment operations,
// resolving the correct subscription client when a nested deployment crosses subscriptions.
func fetchOperationsFor(ctx context.Context, getClient OperationsClientGetter, subscriptionID, resourceGroup, deploymentName string) ([]Operation, error) {
	ops, _, err := fetchOperationTree(ctx, getClient, subscriptionID, resourceGroup, deploymentName)
	return ops, err
}

// fetchOperationTree is fetchOperationsFor, also reporting whether this deployment's own operations were all
// fetched. Errors fetching the operations of nested deployments don't count; those are marked on the nested
// deployment's operation, so that the error closest to what's missing is the one kept as its explanation.
func fetchOperationTree(ctx context.Context, getClient OperationsClientGetter, subscriptionID, resourceGroup, deploymentName string) ([]Operation, bool, error) {
	client, err := getClient(subscriptionID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get operations client for subscription: %w", err)
	}

	// Errors are collected rather than returned immediately: when a deployment failed, whatever we managed to
	// fetch is more useful than nothing, so callers get partial results alongside the error.
	var errs []error
	var operations []Operation
	complete := true
	collect := func(items []*armresources.DeploymentOperation) {
		for _, item := range items {
			op, err := operationFor(item, subscriptionID)
			if err != nil {
				errs = append(errs, err)
				complete = false
				continue
			}
			if op != nil {
				operations = append(operations, *op)
			}
		}
	}
	if resourceGroup != "" {
		pager := client.NewListPager(resourceGroup, deploymentName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to fetch operations: %w", err))
				complete = false
				break
			}
			collect(page.Value)
		}
	} else {
		pager := client.NewListAtSubscriptionScopePager(deploymentName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to fetch operations: %w", err))
				complete = false
				break
			}
			collect(page.Value)
		}
	}

	// Recurse into nested deployments, which may target a different subscription
	skipped := false
	for i, op := range operations {
		if op.Resource == nil {
			continue
		}
		if strings.EqualFold(op.Resource.ResourceType, "Microsoft.Resources/deployments") {
			if skipped || ctx.Err() != nil {
				if !skipped {
					errs = append(errs, fmt.Errorf("skipped fetching operations for remaining child deployments: %w", context.Cause(ctx)))
					skipped = true
				}
				// nothing beneath a skipped deployment is known, so its failures may be unexplained
				operations[i].ChildrenIncomplete = true
				continue
			}
			// Use the child's subscription if available, otherwise inherit the parent's
			childSub := subscriptionID
			if op.Resource.SubscriptionID != "" {
				childSub = op.Resource.SubscriptionID
			}
			children, childrenComplete, err := fetchOperationTree(ctx, getClient, childSub, op.Resource.ResourceGroup, op.Resource.Name)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to fetch operations for child deployment %s/%s: %w", op.Resource.ResourceGroup, op.Resource.Name, err))
			}
			operations[i].Children = children
			operations[i].ChildrenIncomplete = !childrenComplete
		}
	}
	return operations, complete, errors.Join(errs...)
}

// operationFor converts an ARM DeploymentOperation into our internal Operation type.
// Extracts subscription ID from the resource's ARM ID to enable cross-subscription traversal.
// ARM may omit any of the optional fields, so none of them are assumed to be present.
func operationFor(item *armresources.DeploymentOperation, subscriptionID string) (*Operation, error) {
	if item == nil || item.Properties == nil {
		return nil, nil
	}
	props := item.Properties
	var resource *Resource
	if props.TargetResource != nil && props.TargetResource.ID != nil {
		resourceId, err := azcorearm.ParseResourceID(*props.TargetResource.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resource id: %w", err)
		}
		resource = &Resource{
			ResourceType:   resourceId.ResourceType.String(),
			SubscriptionID: resourceId.SubscriptionID,
			ResourceGroup:  resourceId.ResourceGroupName,
			Name:           resourceId.Name,
		}
	}

	op := &Operation{
		OperationType:     string(ptr.Deref(props.ProvisioningOperation, "")),
		Duration:          ptr.Deref(props.Duration, ""),
		ProvisioningState: ptr.Deref(props.ProvisioningState, ""),
		StatusCode:        ptr.Deref(props.StatusCode, ""),
		Resource:          resource,
	}
	if props.Timestamp != nil {
		op.StartTimestamp = props.Timestamp.Format(time.RFC3339)
	}
	if props.StatusMessage != nil && props.StatusMessage.Error != nil {
		redact := []string{subscriptionID}
		if resource != nil {
			redact = append(redact, resource.SubscriptionID)
		}
		op.Error = operationErrorFor(props.StatusMessage.Error, redact)
	}
	return op, nil
}

// maxOperationErrorDepth bounds recursion into ARM error details; real errors are a handful of levels deep.
const maxOperationErrorDepth = 10

func operationErrorFor(e *armresources.ErrorResponse, redact []string) *OperationError {
	return operationErrorForDepth(e, redact, 0)
}

func operationErrorForDepth(e *armresources.ErrorResponse, redact []string, depth int) *OperationError {
	if e == nil {
		return nil
	}
	out := &OperationError{
		Code:    ptr.Deref(e.Code, ""),
		Message: redactSubscriptionIDs(ptr.Deref(e.Message, ""), redact),
		Target:  redactSubscriptionIDs(ptr.Deref(e.Target, ""), redact),
	}
	if depth >= maxOperationErrorDepth {
		return out
	}
	for _, d := range e.Details {
		if child := operationErrorForDepth(d, redact, depth+1); child != nil {
			out.Details = append(out.Details, *child)
		}
	}
	return out
}

var subscriptionPathPattern = regexp.MustCompile(`(?i)(/subscriptions/)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// redactSubscriptionIDs removes subscription IDs from ARM error text before it is logged or written to artifacts.
// Resource IDs are redacted by pattern; the subscriptions we know about are also redacted wherever they appear,
// since resource providers embed them in free-form messages, too.
func redactSubscriptionIDs(s string, known []string) string {
	if s == "" {
		return s
	}
	s = subscriptionPathPattern.ReplaceAllString(s, "${1}<redacted>")
	for _, id := range known {
		if id == "" {
			continue
		}
		s = regexp.MustCompile("(?i)"+regexp.QuoteMeta(id)).ReplaceAllString(s, "<redacted>")
	}
	return s
}

// String renders the error and its nested details, innermost last.
func (e *OperationError) String() string {
	var b strings.Builder
	var write func(e *OperationError, depth int)
	write = func(e *OperationError, depth int) {
		if depth > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Repeat("  ", depth))
		}
		b.WriteString(e.Code)
		if e.Message != "" {
			b.WriteString(": ")
			b.WriteString(e.Message)
		}
		if e.Target != "" {
			b.WriteString(" (target: ")
			b.WriteString(e.Target)
			b.WriteString(")")
		}
		for i := range e.Details {
			write(&e.Details[i], depth+1)
		}
	}
	write(e, 0)
	return b.String()
}

// innermostCodes returns the codes of the leaf errors, which name the actual cause: the outer layers are usually
// generic wrappers like DeploymentFailed or ResourceDeploymentFailure.
func (e *OperationError) innermostCodes() []string {
	if len(e.Details) == 0 {
		return []string{e.Code}
	}
	var codes []string
	for i := range e.Details {
		codes = append(codes, e.Details[i].innermostCodes()...)
	}
	return codes
}

// FailedOperation is a failed leaf operation found in a deployment's operation tree.
type FailedOperation struct {
	// DeploymentPath lists the nested deployment names leading to the operation, outermost first.
	DeploymentPath []string
	Operation      Operation
}

// FailedOperations walks the operation tree and returns the failed operations that explain the failure. Children
// are always searched, since ARM may leave a failed parent's state and error out. A failed nested deployment is
// reported itself only while something beneath it is still unexplained: either nothing beneath it failed, or a bare
// "Failed" beneath it has no closer error, or the operations beneath it could not all be fetched. A generic wrapper over explained failures is left out, but an error that
// may be the only explanation for a bare failure (e.g. a nested status wrapped as JSON text) is kept, and it then
// explains that failure for every ancestor.
func FailedOperations(ops []Operation) []FailedOperation {
	// walk returns the failures to report and whether any of them is still without an explaining error.
	var walk func(ops []Operation, path []string) ([]FailedOperation, bool)
	walk = func(ops []Operation, path []string) ([]FailedOperation, bool) {
		var out []FailedOperation
		unexplained := false
		for _, op := range ops {
			childPath := path
			if op.Resource != nil {
				childPath = append(slices.Clone(path), op.Resource.Name)
			}
			beneath, beneathUnexplained := walk(op.Children, childPath)
			failed := op.Error != nil || strings.EqualFold(op.ProvisioningState, string(armresources.ProvisioningStateFailed))
			// the operations we couldn't fetch may hold failures that nothing beneath explains, unless the deployment is
			// known to have succeeded (a sparse one, without a state, may still be the failed one)
			succeeded := strings.EqualFold(op.ProvisioningState, string(armresources.ProvisioningStateSucceeded))
			beneathUnexplained = beneathUnexplained || (!succeeded && op.ChildrenIncomplete)
			report := false
			switch {
			case !failed:
				unexplained = unexplained || beneathUnexplained
			case op.Error != nil:
				// this error explains whatever beneath it was not explained yet
				report = len(beneath) == 0 || beneathUnexplained
			case len(beneath) == 0:
				report, unexplained = true, true
			default:
				unexplained = unexplained || beneathUnexplained
			}
			if report {
				leaf := op
				leaf.Children = nil
				out = append(out, FailedOperation{DeploymentPath: slices.Clone(path), Operation: leaf})
			}
			out = append(out, beneath...)
		}
		return out, unexplained
	}
	out, _ := walk(ops, nil)
	return out
}

// fetchSubscriptionScopedOperationsFor is a convenience entry point for subscription-scoped
// deployments (no resource group). Delegates to fetchOperationsFor with an empty resource group.
func fetchSubscriptionScopedOperationsFor(ctx context.Context, getClient OperationsClientGetter, subscriptionID, deploymentName string) ([]Operation, error) {
	return fetchOperationsFor(ctx, getClient, subscriptionID, "", deploymentName)
}
