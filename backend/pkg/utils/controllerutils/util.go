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
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ServiceProviderDefaultIngressWildcardServingCertName is the Kubernetes Secret and
// SecretProviderClass name used for the default ingress wildcard serving certificate
// referenced by HostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.
const ServiceProviderDefaultIngressWildcardServingCertName = "service-provider-default-ingress-wildcard-serving-cert"

type Controller interface {
	QueueForInformers(resyncDuration time.Duration, notifiers ...Notifier) error
	SyncOnce(ctx context.Context, keyObj any) error
	Run(ctx context.Context, threadiness int)
}

// WatchingController exposes resource-event filtering on the underlying generic
// watching controller. With no interests registered, all events are accepted.
type WatchingController interface {
	Controller
	AddInterestingResource(resourceType azcorearm.ResourceType, resourceName string)
	// QueueForChildInformers maps direct child events (including deletion) to
	// this controller's owner key, respecting its resource-interest filter.
	QueueForChildInformers(resyncDuration time.Duration, notifiers ...Notifier) error
	QueueForDescendantInformers(time.Duration, int, ...Notifier) error
}

// OperationKey is for driving workqueues keyed for operations
type OperationKey struct {
	SubscriptionID   string `json:"subscriptionID"`
	OperationName    string `json:"operationName"`
	ParentResourceID string `json:"parentResourceID"`
}

func (k OperationKey) GetParentResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(k.ParentResourceID))
}

func (k OperationKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetParentResourceID()).
			AddOperationID(k.OperationName)...)
}

func (k OperationKey) InitialController(controllerName string) *coreapi.Controller {
	// TODO, this structure only allows one status per operation controller even if there are multiple instances of the operation
	// TODO, this may or may not age well. Nesting is possible or we could actually separate controllers that way (probably useful).
	// TODO, leaving this as a thing open to change in the future.
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(k.GetParentResourceID().String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: k.GetParentResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// HCPClusterKey is for driving workqueues keyed for clusters
type HCPClusterKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
}

func (k HCPClusterKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToClusterResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName))
}

func (k HCPClusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k HCPClusterKey) InitialController(controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: k.GetResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// HCPNodePoolKey is for driving workqueus keyed for nodepools
type HCPNodePoolKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
	HCPNodePoolName   string `json:"hcpNodePoolName"`
}

func (k HCPNodePoolKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToNodePoolResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName, k.HCPNodePoolName))
}

func (k HCPNodePoolKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k HCPNodePoolKey) InitialController(controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: k.GetResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// SubscriptionKey is for driving workqueues keyed for subscriptions
type SubscriptionKey struct {
	SubscriptionID string `json:"subscriptionID"`
}

func (k SubscriptionKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToSubscriptionResourceID(k.SubscriptionID))
}

func (k SubscriptionKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

type HCPExternalAuthKey struct {
	SubscriptionID      string `json:"subscriptionID"`
	ResourceGroupName   string `json:"resourceGroupName"`
	HCPClusterName      string `json:"hcpClusterName"`
	HCPExternalAuthName string `json:"hcpExternalAuthName"`
}

func (k *HCPExternalAuthKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToExternalAuthResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName, k.HCPExternalAuthName))
}

func (k *HCPExternalAuthKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k *HCPExternalAuthKey) InitialController(controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: k.GetResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// SystemAdminCredentialRequestKey is for driving workqueues keyed for credential requests
type SystemAdminCredentialRequestKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
	CredentialName    string `json:"credentialName"`
}

func (k SystemAdminCredentialRequestKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToSystemAdminCredentialRequestResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName, k.CredentialName))
}

func (k SystemAdminCredentialRequestKey) GetClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToClusterResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName))
}

func (k SystemAdminCredentialRequestKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k SystemAdminCredentialRequestKey) InitialController(controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: k.GetResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// SystemAdminCredentialRevocationKey is for driving workqueues keyed for revocations
type SystemAdminCredentialRevocationKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
	RevocationName    string `json:"revocationName"`
}

func (k SystemAdminCredentialRevocationKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToSystemAdminCredentialRevocationResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName, k.RevocationName))
}

func (k SystemAdminCredentialRevocationKey) GetClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(coreapihelpers.ToClusterResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName))
}

func (k SystemAdminCredentialRevocationKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k SystemAdminCredentialRevocationKey) InitialController(controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: k.GetResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// controllerMutationFunc is called when trying to write a controller. It gives a spot for computation of a value.
// It should only perform short calls, not long lookups.  It must not fail. Think of it as a way to write information
// that you have already precomputed.
type controllerMutationFunc func(ctx context.Context, controller *coreapi.Controller)

const (
	// timestampRegexFragment is the embeddable (non-anchored) date-time pattern shared by the
	// AADSTS and Cluster Service timestamp regexes below. It's deliberately looser than strict
	// ISO 8601 (it accepts a plain space instead of "T" as the date/time separator, since that's
	// the form AADSTS actually emits). Matches e.g. "2026-11-02 15:49:40Z",
	// "2026-11-02T15:49:40.123+00:00", and "2026-11-02T15:49:40.1234567Z" (arbitrary
	// fractional-second precision).
	timestampRegexFragment = `\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`
)

var (
	// Matches the inline "Timestamp: <date> <time>" text that Azure AD STS (AADSTS, the Azure AD
	// Security Token Service) embeds in error_description, e.g. "Timestamp: 2026-11-02
	// 15:49:40Z" or "...T15:49:40.123+00:00". Stops at the end of the timestamp value so trailing
	// text (closing quote, comma, extra sentence) is untouched.
	aadstsInlineTimestampRegex = regexp.MustCompile(`(?i)Timestamp:\s*` + timestampRegexFragment)
	// Matches the inline "Trace ID: <value>" text in error_description. The value is any run of
	// alphanumerics and hyphens, which covers GUIDs and similar ID shapes while still stopping
	// at punctuation (closing quote, comma, sentence-ending period) that isn't part of the ID.
	aadstsInlineTraceIDRegex = regexp.MustCompile(`(?i)Trace ID:\s*[0-9a-zA-Z-]+`)
	// Same as aadstsInlineTraceIDRegex, but for the inline "Correlation ID: <value>" text.
	aadstsInlineCorrelationIDRegex = regexp.MustCompile(`(?i)Correlation ID:\s*[0-9a-zA-Z-]+`)
	// Matches the tenant expiration error pattern "The tenant 'X' is not available due to expiration".
	// The reason we do this is because we have observed that the same API call for the same tenant returning this error
	// sometimes returns the tenant GUID and sometimes the tenant display name. This redacts the tenant
	// identifier to prevent the same error for the same tenant from producing different error messages.
	aadstsTenantExpirationRegex = regexp.MustCompile(`(?i)The tenant '[^']*' is not available due to expiration`)
	// Matches the raw JSON field "timestamp": "<value>" in the AADSTS response body.
	aadstsJSONTimestampRegex = regexp.MustCompile(`"timestamp"\s*:\s*"[^"]*"`)
	// Matches the raw JSON field "trace_id": "<value>" in the AADSTS response body.
	aadstsJSONTraceIDRegex = regexp.MustCompile(`"trace_id"\s*:\s*"[^"]*"`)
	// Matches the raw JSON field "correlation_id": "<value>" in the AADSTS response body.
	aadstsJSONCorrelationIDRegex = regexp.MustCompile(`"correlation_id"\s*:\s*"[^"]*"`)
	// Matches Azure Resource Manager (ARM) throttling messages like "Retry after 3 seconds."
	// Only the number is redacted; the unit is captured and kept as-is in the replacement,
	// since it's unverified whether ARM ever reports the wait in a different unit (minutes,
	// ms, etc.) and the output shouldn't claim a unit that wasn't actually there.
	armRetryAfterRegex = regexp.MustCompile(`(?i)Retry after \d+(?:\.\d+)?\s*([a-z]+)`)
	// Matches the ARM SubscriptionRequestsThrottled message body's "try again after '<n>'
	// seconds" text, e.g. "Please try again after '10' seconds after additional tokens are
	// available." Only the quoted number is redacted; the unit is captured and kept as-is in
	// the replacement for the same reason as armRetryAfterRegex above.
	armTryAgainAfterRegex = regexp.MustCompile(`(?i)try again after '\d+(?:\.\d+)?' ([a-z]+)`)
	// Matches the OCM Cluster Service (CS) client's "at '<timestamp>'" text, e.g.
	// "at '2026-10-22T16:59:33Z'", as seen in PostCluster/client.go error wrapping.
	csTimestampRegex = regexp.MustCompile(`(?i)at '` + timestampRegexFragment + `'`)
	// Matches the Cluster Service client's "operation identifier is '<uuid>'" text.
	csOperationIDRegex = regexp.MustCompile(`(?i)operation identifier is '[^']*'`)
)

// RedactDynamicSyncErrorMessageFields strips values that change on every retry but don't reflect a
// different underlying failure (Azure AD Security Token Service (AADSTS) timestamp/trace
// ID/correlation ID, Azure Resource Manager (ARM) throttling retry-after durations, OCM
// Cluster Service timestamp/operation identifier) from an error message, both inline text and
// raw JSON fields. This keeps an otherwise identical failure from producing a different
// persisted message on every retry. Left unredacted, repeatedly writing a changing message to a
// Cosmos document (e.g. a controller's Degraded condition, or a field like
// ServiceProviderCluster's per-identity RetrievalError) can itself trigger other controllers
// watching that document to sync, fail again for the same underlying cause but with a new
// message, and write again, repeating the cycle in a write amplification feedback loop that
// never settles.
func RedactDynamicSyncErrorMessageFields(message string) string {
	message = aadstsInlineTimestampRegex.ReplaceAllString(message, "Timestamp: <removed>")
	message = aadstsInlineTraceIDRegex.ReplaceAllString(message, "Trace ID: <removed>")
	message = aadstsInlineCorrelationIDRegex.ReplaceAllString(message, "Correlation ID: <removed>")
	message = aadstsTenantExpirationRegex.ReplaceAllString(message, "The tenant '<removed>' is not available due to expiration")
	message = aadstsJSONTimestampRegex.ReplaceAllString(message, `"timestamp": "<removed>"`)
	message = aadstsJSONTraceIDRegex.ReplaceAllString(message, `"trace_id": "<removed>"`)
	message = aadstsJSONCorrelationIDRegex.ReplaceAllString(message, `"correlation_id": "<removed>"`)
	message = armRetryAfterRegex.ReplaceAllString(message, "Retry after <removed> $1")
	message = armTryAgainAfterRegex.ReplaceAllString(message, "try again after '<removed>' $1")
	message = csTimestampRegex.ReplaceAllString(message, "at '<removed>'")
	message = csOperationIDRegex.ReplaceAllString(message, "operation identifier is '<removed>'")
	return message
}

func ReportSyncError(syncErr error) controllerMutationFunc {
	return func(ctx context.Context, controller *coreapi.Controller) {
		if syncErr == nil {
			meta.SetStatusCondition(&controller.Status.Conditions, metav1.Condition{
				Type:    "Degraded",
				Status:  metav1.ConditionFalse,
				Reason:  "NoErrors",
				Message: "As expected.",
			})
			return
		}

		// We log the error before redacting the dynamic fields so we can see the original error message.
		logger := utils.LoggerFromContext(ctx)
		logger.Error(syncErr, "had an error while syncing")

		syncErrorMessage := syncErr.Error()
		syncErrorMessage = RedactDynamicSyncErrorMessageFields(syncErrorMessage)

		meta.SetStatusCondition(&controller.Status.Conditions, metav1.Condition{
			Type:    "Degraded",
			Status:  metav1.ConditionTrue,
			Reason:  "Failed",
			Message: fmt.Sprintf("Had an error while syncing: %s", syncErrorMessage),
		})
	}
}

// InitialControllerFunc builds a new coreapi.Controller for the given logical controller name
// (for example HCPClusterKey.InitialController).
type InitialControllerFunc func(controllerName string) *coreapi.Controller

func DegradedControllerPanicHandler(ctx context.Context, controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller], controllerName string, initialControllerFn InitialControllerFunc) func(interface{}) {
	return func(panicVal interface{}) {
		stack := debug.Stack()
		err := WriteController(ctx, controllerCRUD, controllerName, initialControllerFn, ReportSyncError(fmt.Errorf("panic caught:\n%v\n\n%s", panicVal, stack)))
		if err != nil {
			logger := utils.LoggerFromContext(ctx)
			logger.Error(err, "failed to write controller after panic")
		}
	}
}

func controllerCRUDForParent(resourcesDBClient corecosmosstorage.ResourcesDBClient, parentResourceID *azcorearm.ResourceID) (cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller], error) {
	subscriptionID := parentResourceID.SubscriptionID
	resourceGroupName := parentResourceID.ResourceGroupName
	hcp := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)

	switch {
	case metadataapi.ResourceTypeEqual(parentResourceID.ResourceType, coreapi.ClusterResourceType):
		return hcp.Controllers(parentResourceID.Name), nil
	case metadataapi.ResourceTypeEqual(parentResourceID.ResourceType, coreapi.NodePoolResourceType):
		if parentResourceID.Parent == nil {
			return nil, fmt.Errorf("node pool resource ID is missing parent cluster ID")
		}
		clusterName := parentResourceID.Parent.Name
		return hcp.NodePools(clusterName).Controllers(parentResourceID.Name), nil
	case metadataapi.ResourceTypeEqual(parentResourceID.ResourceType, coreapi.ExternalAuthResourceType):
		if parentResourceID.Parent == nil {
			return nil, fmt.Errorf("external auth resource ID is missing parent cluster ID")
		}
		clusterName := parentResourceID.Parent.Name
		return hcp.ExternalAuth(clusterName).Controllers(parentResourceID.Name), nil
	default:
		return nil, fmt.Errorf("unsupported parent resource type for controllers: %s", parentResourceID.ResourceType)
	}
}

// getOrCreateControllerDocument returns the controller document from Cosmos, creating it with
// initialControllerFn if missing. On create conflict (HTTP 409), it re-reads and returns the
// existing document (same pattern as corecosmosstorage.GetOrCreateServiceProviderCluster).
func getOrCreateControllerDocument(
	ctx context.Context,
	controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller],
	controllerName string,
	initialControllerFn InitialControllerFunc,
	secondAttempt ...bool,
) (*coreapi.Controller, error) {
	if initialControllerFn == nil {
		return nil, fmt.Errorf("initialControllerFn is required")
	}

	existingController, err := controllerCRUD.Get(ctx, controllerName)
	switch {
	case err == nil:
		return existingController, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	existingController, err = controllerCRUD.Create(ctx, initialControllerFn(controllerName), nil)
	switch {
	case err == nil:
		return existingController, nil
	case cosmosstorageutils.IsConflictError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	existingController, err = controllerCRUD.Get(ctx, controllerName)
	switch {
	case err == nil:
		return existingController, nil
	case cosmosstorageutils.IsNotFoundError(err):
		if len(secondAttempt) >= 1 && secondAttempt[0] {
			return nil, utils.TrackError(fmt.Errorf("second NotFound, Conflict, NotFound error: %w", err))
		}
		timer := time.NewTimer((cosmosstorageutils.SoftDeleteTTLSeconds + 1) * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, utils.TrackError(ctx.Err())
		case <-timer.C:
			// This can happen when the soft-delete marks an item, the GET will return 404, the create will 409, the second get will 404.
			// By waiting longer than the cosmos TTL, we can re-enter the loop and try again later.  This is a rare case and will
			// only happen when the parent item exists and the controller was deleted.
			return getOrCreateControllerDocument(ctx, controllerCRUD, controllerName, initialControllerFn, true)
		}
	default:
		return nil, utils.TrackError(err)
	}
}

// GetOrCreateController gets the named Controller document under the given parent resource
// (cluster, node pool, or external auth). If it does not exist, it creates one using initialControllerFn.
// On create conflict (HTTP 409), it re-reads and returns the existing document (same pattern as
// corecosmosstorage.GetOrCreateServiceProviderCluster).
func GetOrCreateController(
	ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient, parentResourceID *azcorearm.ResourceID,
	controllerName string, initialControllerFn InitialControllerFunc,
) (*coreapi.Controller, error) {
	controllerCRUD, err := controllerCRUDForParent(resourcesDBClient, parentResourceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	doc, err := getOrCreateControllerDocument(ctx, controllerCRUD, controllerName, initialControllerFn)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return doc, nil
}

// WriteController will read the existing value, call the mutations in order, then write the result.  It only tries *once*.
// If it fails, then the an error is returned.  This detail is important, it doesn't even retry conflicts.  This is so that
// if a failure happens the control-loop will re-run and restablish the information it was trying to write as valid.
// This prevents accidental recreation of controller instances in cosmos during a delete.
func WriteController(ctx context.Context, controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller], controllerName string, initialControllerFn InitialControllerFunc, mutationFns ...controllerMutationFunc) error {
	logger := utils.LoggerFromContext(ctx)

	existingController, err := getOrCreateControllerDocument(ctx, controllerCRUD, controllerName, initialControllerFn)
	if err != nil {
		return err
	}

	desiredController := existingController.DeepCopy()
	for _, mutationFn := range mutationFns {
		mutationFn(ctx, desiredController)
	}

	if equality.Semantic.DeepEqual(existingController, desiredController) {
		// nothing to report.
		return nil
	}

	_, replaceErr := controllerCRUD.Replace(ctx, desiredController, nil)
	if replaceErr != nil {
		logger.Error(replaceErr, "failed to replace")
		return fmt.Errorf("failed to replace existing controller state: %w", replaceErr)
	}
	return nil
}

func ClusterServiceIDForCluster(cluster *coreapi.Cluster) string {
	if cluster.ServiceProviderProperties.PendingClusterServiceID != nil {
		return cluster.ServiceProviderProperties.PendingClusterServiceID.ClusterID()
	}
	if cluster.ServiceProviderProperties.ClusterServiceID != nil {
		return cluster.ServiceProviderProperties.ClusterServiceID.ClusterID()
	}
	return ""
}
