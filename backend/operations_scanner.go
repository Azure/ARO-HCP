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

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/tracing"
)

const (
	defaultSubscriptionConcurrency   = 10
	defaultPollIntervalSubscriptions = 10 * time.Minute
	defaultPollIntervalOperations    = 10 * time.Second

	// Check listOperationLabelValues() if adding more constants.
	collectSubscriptionsLabel      = "list_subscriptions"
	processSubscriptionsLabel      = "process_subscriptions"
	processOperationsLabel         = "process_operations"
	pollClusterOperationLabel      = "poll_cluster"
	pollNodePoolOperationLabel     = "poll_node_pool"
	pollBreakGlassCredential       = "poll_break_glass_credential"
	pollBreakGlassCredentialRevoke = "poll_break_glass_credential_revoke"

	tracerName = "github.com/Azure/ARO-HCP/backend"
)

// Copied from uhc-clusters-service, because the
// OCM SDK does not define this for some reason.
type NodePoolStateValue string

const (
	NodePoolStateValidating       NodePoolStateValue = "validating"
	NodePoolStatePending          NodePoolStateValue = "pending"
	NodePoolStateInstalling       NodePoolStateValue = "installing"
	NodePoolStateReady            NodePoolStateValue = "ready"
	NodePoolStateUpdating         NodePoolStateValue = "updating"
	NodePoolStateValidatingUpdate NodePoolStateValue = "validating_update"
	NodePoolStatePendingUpdate    NodePoolStateValue = "pending_update"
	NodePoolStateUninstalling     NodePoolStateValue = "uninstalling"
	NodePoolStateRecoverableError NodePoolStateValue = "recoverable_error"
	NodePoolStateError            NodePoolStateValue = "error"
)

const (
	InflightChecksFailedProvisionErrorCode = "OCM4001"
)

type operation struct {
	id     string
	pk     azcosmos.PartitionKey
	doc    *database.OperationDocument
	logger *slog.Logger
}

// listOperationLabelValues returns an iterator that yields all recognized
// label values for operation metrics.
func listOperationLabelValues() iter.Seq[string] {
	return slices.Values([]string{
		collectSubscriptionsLabel,
		processSubscriptionsLabel,
		processOperationsLabel,
		pollClusterOperationLabel,
		pollNodePoolOperationLabel,
		pollBreakGlassCredential,
		pollBreakGlassCredentialRevoke,
	})
}

// setSpanAttributes sets the operation and resource attributes on the span.
func (o *operation) setSpanAttributes(span trace.Span) {
	// Operation attributes.
	span.SetAttributes(
		tracing.OperationIDKey.String(string(o.id)),
		tracing.OperationTypeKey.String(string(o.doc.Request)),
		tracing.OperationStatusKey.String(string(o.doc.Status)),
	)

	// Resource attributes.
	if o.doc.ExternalID != nil {
		span.SetAttributes(
			tracing.ResourceGroupNameKey.String(o.doc.ExternalID.ResourceGroupName),
			tracing.ResourceNameKey.String(o.doc.ExternalID.Name),
			tracing.ResourceTypeKey.String(o.doc.ExternalID.ResourceType.Type),
		)
	}

	switch o.doc.InternalID.Kind() {
	case arohcpv1alpha1.ClusterKind:
		span.SetAttributes(tracing.ClusterIDKey.String(o.doc.InternalID.ID()))
		// TODO(simonpasquier): add node pool attribute when available
	}
}

type OperationsScanner struct {
	dbClient            database.DBClient
	lockClient          *database.LockClient
	clusterService      ocm.ClusterServiceClient
	notificationClient  *http.Client
	subscriptionsLock   sync.Mutex
	subscriptions       []string
	subscriptionChannel chan string
	subscriptionWorkers sync.WaitGroup

	leaderGauge            prometheus.Gauge
	workerGauge            prometheus.Gauge
	operationsCount        *prometheus.CounterVec
	operationsFailedCount  *prometheus.CounterVec
	operationsDuration     *prometheus.HistogramVec
	lastOperationTimestamp *prometheus.GaugeVec
	subscriptionsByState   *prometheus.GaugeVec
}

func NewOperationsScanner(dbClient database.DBClient, ocmConnection *ocmsdk.Connection) *OperationsScanner {
	s := &OperationsScanner{
		dbClient:           dbClient,
		lockClient:         dbClient.GetLockClient(),
		clusterService:     ocm.ClusterServiceClient{Conn: ocmConnection},
		notificationClient: http.DefaultClient,
		subscriptions:      make([]string, 0),

		leaderGauge: promauto.With(prometheus.DefaultRegisterer).NewGauge(
			prometheus.GaugeOpts{
				Name: "backend_leader_election_state",
				Help: "Leader election state (1 when leader).",
			},
		),
		workerGauge: promauto.With(prometheus.DefaultRegisterer).NewGauge(
			prometheus.GaugeOpts{
				Name: "backend_workers",
				Help: "Number of concurrent workers.",
			},
		),
		operationsCount: promauto.With(prometheus.DefaultRegisterer).NewCounterVec(
			prometheus.CounterOpts{
				Name: "backend_operations_total",
				Help: "Total count of operations.",
			},
			[]string{"type"},
		),
		operationsFailedCount: promauto.With(prometheus.DefaultRegisterer).NewCounterVec(
			prometheus.CounterOpts{
				Name: "backend_failed_operations_total",
				Help: "Total count of failed operations.",
			},
			[]string{"type"},
		),
		operationsDuration: promauto.With(prometheus.DefaultRegisterer).NewHistogramVec(
			prometheus.HistogramOpts{
				Name:                            "backend_operations_duration_seconds",
				Help:                            "Histogram of operation latencies.",
				Buckets:                         []float64{.25, .5, 1, 2, 5},
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: 1 * time.Hour,
			},
			[]string{"type"},
		),
		lastOperationTimestamp: promauto.With(prometheus.DefaultRegisterer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_last_operation_timestamp_seconds",
				Help: "Timestamp of the last operation.",
			},
			[]string{"type"},
		),
		subscriptionsByState: promauto.With(prometheus.DefaultRegisterer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_subscriptions",
				Help: "Number of subscriptions by state.",
			},
			[]string{"state"},
		),
	}

	// Initialize the counter and histogram metrics.
	for v := range listOperationLabelValues() {
		s.operationsCount.WithLabelValues(v)
		s.operationsFailedCount.WithLabelValues(v)
		s.operationsDuration.WithLabelValues(v)
		s.lastOperationTimestamp.WithLabelValues(v)
	}

	for subscriptionState := range arm.ListSubscriptionStates() {
		s.subscriptionsByState.WithLabelValues(string(subscriptionState))
	}

	return s
}

// getInterval parses an environment variable into a time.Duration value.
// If the environment variable is not defined or its value is invalid,
// getInternal returns defaultVal.
func getInterval(envName string, defaultVal time.Duration, logger *slog.Logger) time.Duration {
	if intervalString, ok := os.LookupEnv(envName); ok {
		interval, err := time.ParseDuration(intervalString)
		if err == nil {
			return interval
		} else {
			logger.Warn(fmt.Sprintf("Cannot use %s: %v", envName, err.Error()))
		}
	}
	return defaultVal
}

// getPositiveInt parses an environment variable into a positive integer.
// If the environment variable is not defined or its value is invalid,
// getPositiveInt returns defaultVal.
func getPositiveInt(envName string, defaultVal int, logger *slog.Logger) int {
	if intString, ok := os.LookupEnv(envName); ok {
		positiveInt, err := strconv.Atoi(intString)
		if err == nil && positiveInt <= 0 {
			err = errors.New("value must be positive")
		}

		if err == nil {
			return positiveInt
		}

		logger.Warn(fmt.Sprintf("Cannot use %s: %v", envName, err.Error()))
	}

	return defaultVal
}

// Run executes the main loop of the OperationsScanner.
func (s *OperationsScanner) Run(ctx context.Context, logger *slog.Logger) {
	var interval time.Duration

	interval = getInterval("BACKEND_POLL_INTERVAL_SUBSCRIPTIONS", defaultPollIntervalSubscriptions, logger)
	logger.Info("Polling subscriptions in Cosmos DB every " + interval.String())
	collectSubscriptionsTicker := time.NewTicker(interval)

	interval = getInterval("BACKEND_POLL_INTERVAL_OPERATIONS", defaultPollIntervalOperations, logger)
	logger.Info("Polling operations in Cosmos DB every " + interval.String())
	processSubscriptionsTicker := time.NewTicker(interval)

	numWorkers := getPositiveInt("BACKEND_SUBSCRIPTION_CONCURRENCY", defaultSubscriptionConcurrency, logger)
	logger.Info(fmt.Sprintf("Processing %d subscriptions at a time", numWorkers))
	s.workerGauge.Set(float64(numWorkers))

	// Create a buffered channel using worker pool size as a heuristic.
	s.subscriptionChannel = make(chan string, numWorkers)
	defer close(s.subscriptionChannel)

	// In this worker pool, each worker processes all operations within
	// a single Azure subscription / Cosmos DB partition.
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer s.subscriptionWorkers.Done()
			for subscriptionID := range s.subscriptionChannel {
				subscriptionLogger := logger.With("subscription_id", subscriptionID)
				s.withSubscriptionLock(ctx, subscriptionLogger, subscriptionID, func(ctx context.Context) {
					s.processOperations(ctx, subscriptionID, subscriptionLogger)
				})
			}
		}()
	}
	s.subscriptionWorkers.Add(numWorkers)

	// Collect subscriptions immediately on startup.
	s.collectSubscriptions(ctx, logger)

loop:
	for {
		select {
		case <-collectSubscriptionsTicker.C:
			s.collectSubscriptions(ctx, logger)
		case <-processSubscriptionsTicker.C:
			s.processSubscriptions(ctx, logger)
		case <-ctx.Done():
			// break alone just breaks out of select.
			// Use a label to break out of the loop.
			break loop
		}
	}
}

// Join waits for the OperationsScanner to gracefully shut down.
func (s *OperationsScanner) Join() {
	s.subscriptionWorkers.Wait()
	s.leaderGauge.Set(0)
}

// updateOperationMetrics records counter and latency metrics for operations.
func (s *OperationsScanner) updateOperationMetrics(label string) func() {
	startTime := time.Now()
	s.operationsCount.WithLabelValues(label).Inc()
	return func() {
		s.operationsDuration.WithLabelValues(label).Observe(time.Since(startTime).Seconds())
		s.lastOperationTimestamp.WithLabelValues(label).SetToCurrentTime()
	}
}

// collectSubscriptions builds an internal list of Azure subscription IDs by
// querying Cosmos DB.
func (s *OperationsScanner) collectSubscriptions(ctx context.Context, logger *slog.Logger) {
	ctx, span := startRootSpan(ctx, "collectSubscriptions")
	defer span.End()
	defer s.updateOperationMetrics(collectSubscriptionsLabel)()

	var subscriptions []string

	iterator := s.dbClient.ListAllSubscriptionDocs()

	subscriptionStates := map[arm.SubscriptionState]int{}
	for subscriptionState := range arm.ListSubscriptionStates() {
		subscriptionStates[subscriptionState] = 0
	}
	for subscriptionID, subscription := range iterator.Items(ctx) {
		// Unregistered subscriptions should have no active operations,
		// not even deletes.
		if subscription.State != arm.SubscriptionStateUnregistered {
			subscriptions = append(subscriptions, subscriptionID)
		}
		subscriptionStates[subscription.State]++
	}

	span.SetAttributes(tracing.ProcessedItemsKey.Int(len(subscriptions)))
	err := iterator.GetError()
	if err != nil {
		s.recordOperationError(ctx, collectSubscriptionsLabel, err)
		logger.Error(fmt.Sprintf("Error while paging through Cosmos query results: %v", err.Error()))
		return
	}

	s.subscriptionsLock.Lock()
	defer s.subscriptionsLock.Unlock()

	if len(subscriptions) != len(s.subscriptions) {
		logger.Info(fmt.Sprintf("Tracking %d active subscriptions", len(subscriptions)))
	}

	for k, v := range subscriptionStates {
		s.subscriptionsByState.WithLabelValues(string(k)).Set(float64(v))
	}

	s.subscriptions = subscriptions
}

// processSubscriptions feeds the internal list of Azure subscription IDs
// to the worker pool for processing. processSubscriptions may block if the
// worker pool gets overloaded. The log will indicate if this occurs.
func (s *OperationsScanner) processSubscriptions(ctx context.Context, logger *slog.Logger) {
	_, span := startRootSpan(ctx, "processSubscriptions")
	defer span.End()
	defer s.updateOperationMetrics(processSubscriptionsLabel)()

	// This method may block while feeding subscription IDs to the
	// worker pool, so take a clone of the subscriptions slice to
	// iterate over.
	s.subscriptionsLock.Lock()
	subscriptions := slices.Clone(s.subscriptions)
	s.subscriptionsLock.Unlock()

	for _, subscriptionID := range subscriptions {
		select {
		case s.subscriptionChannel <- subscriptionID:
		default:
			// The channel is full. Push the subscription anyway
			// but log how long we block for. This will indicate
			// when the worker pool size needs increased.
			start := time.Now()
			s.subscriptionChannel <- subscriptionID
			logger.Warn(fmt.Sprintf("Subscription processing blocked for %s", time.Since(start)))
		}
	}
}

// processOperations processes all operations in a single Azure subscription.
func (s *OperationsScanner) processOperations(ctx context.Context, subscriptionID string, logger *slog.Logger) {
	ctx, span := startRootSpan(ctx, "processOperations")
	defer span.End()
	defer s.updateOperationMetrics(processOperationsLabel)()
	span.SetAttributes(tracing.SubscriptionIDKey.String(subscriptionID))

	pk := database.NewPartitionKey(subscriptionID)

	iterator := s.dbClient.ListActiveOperationDocs(pk, nil)

	var n int
	for operationID, operationDoc := range iterator.Items(ctx) {
		n++
		s.processOperation(
			ctx,
			operation{
				id:  operationID,
				pk:  pk,
				doc: operationDoc,
				logger: logger.With(
					"operation", operationDoc.Request,
					"operation_id", operationID,
					"resource_id", operationDoc.ExternalID.String(),
					"internal_id", operationDoc.InternalID.String(),
				),
			})
	}
	span.SetAttributes(tracing.ProcessedItemsKey.Int(n))

	err := iterator.GetError()
	if err != nil {
		s.recordOperationError(ctx, processOperationsLabel, err)
		logger.Error(fmt.Sprintf("Error while paging through Cosmos query results: %v", err.Error()))
	}
}

// processOperation processes a single operation on a resource.
func (s *OperationsScanner) processOperation(ctx context.Context, op operation) {
	ctx, span := startChildSpan(ctx, "processOperation")
	defer span.End()

	switch op.doc.InternalID.Kind() {
	case arohcpv1alpha1.ClusterKind:
		switch op.doc.Request {
		case database.OperationRequestRevokeCredentials:
			s.pollBreakGlassCredentialRevoke(ctx, op)
		default:
			s.pollClusterOperation(ctx, op)
		}
	case arohcpv1alpha1.NodePoolKind:
		s.pollNodePoolOperation(ctx, op)
	case cmv1.BreakGlassCredentialKind:
		s.pollBreakGlassCredential(ctx, op)
	}
}

func (s *OperationsScanner) recordOperationError(ctx context.Context, operationName string, err error) {
	if err == nil {
		return
	}

	s.operationsFailedCount.WithLabelValues(operationName).Inc()
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// pollClusterOperation updates the status of a cluster operation.
func (s *OperationsScanner) pollClusterOperation(ctx context.Context, op operation) {
	ctx, span := startChildSpan(ctx, "pollClusterOperation")
	defer span.End()
	defer s.updateOperationMetrics(pollClusterOperationLabel)()
	op.setSpanAttributes(span)

	clusterStatus, err := s.clusterService.GetClusterStatus(ctx, op.doc.InternalID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound && op.doc.Request == database.OperationRequestDelete {
			err = s.setDeleteOperationAsCompleted(ctx, op)
			if err != nil {
				s.recordOperationError(ctx, pollClusterOperationLabel, err)
				op.logger.Error(fmt.Sprintf("Failed to handle a completed deletion: %v", err))
			}
		} else {
			s.recordOperationError(ctx, pollClusterOperationLabel, err)
			op.logger.Error(fmt.Sprintf("Failed to get cluster status: %v", err))
		}

		return
	}

	opStatus, opError, err := s.convertClusterStatus(ctx, op, clusterStatus)
	if err != nil {
		s.recordOperationError(ctx, pollClusterOperationLabel, err)
		op.logger.Warn(err.Error())
		return
	}

	err = s.updateOperationStatus(ctx, op, opStatus, opError)
	if err != nil {
		s.recordOperationError(ctx, pollClusterOperationLabel, err)
		op.logger.Error(fmt.Sprintf("Failed to update operation status: %v", err))
	}
}

// pollNodePoolOperation updates the status of a node pool operation.
func (s *OperationsScanner) pollNodePoolOperation(ctx context.Context, op operation) {
	ctx, span := startChildSpan(ctx, "pollNodePoolOperation")
	defer span.End()
	defer s.updateOperationMetrics(pollNodePoolOperationLabel)()
	op.setSpanAttributes(span)

	nodePoolStatus, err := s.clusterService.GetNodePoolStatus(ctx, op.doc.InternalID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound && op.doc.Request == database.OperationRequestDelete {
			err = s.setDeleteOperationAsCompleted(ctx, op)
			if err != nil {
				s.recordOperationError(ctx, pollNodePoolOperationLabel, err)
				op.logger.Error(fmt.Sprintf("Failed to handle a completed deletion: %v", err))
			}
		} else {
			s.recordOperationError(ctx, pollNodePoolOperationLabel, err)
			op.logger.Error(fmt.Sprintf("Failed to get node pool status: %v", err))
		}

		return
	}

	opStatus, opError, err := convertNodePoolStatus(op, nodePoolStatus)
	if err != nil {
		s.recordOperationError(ctx, pollNodePoolOperationLabel, err)
		op.logger.Warn(err.Error())
		return
	}

	err = s.updateOperationStatus(ctx, op, opStatus, opError)
	if err != nil {
		s.recordOperationError(ctx, pollNodePoolOperationLabel, err)
		op.logger.Error(fmt.Sprintf("Failed to update operation status: %v", err))
	}
}

// pollBreakGlassCredential updates the status of a credential creation operation.
func (s *OperationsScanner) pollBreakGlassCredential(ctx context.Context, op operation) {
	ctx, span := startChildSpan(ctx, "pollBreakGlassCredential")
	defer span.End()
	defer s.updateOperationMetrics(pollBreakGlassCredential)()
	op.setSpanAttributes(span)

	breakGlassCredential, err := s.clusterService.GetBreakGlassCredential(ctx, op.doc.InternalID)
	if err != nil {
		s.recordOperationError(ctx, pollBreakGlassCredential, err)
		op.logger.Error(fmt.Sprintf("Failed to get break-glass credential: %v", err))
		return
	}

	var opStatus arm.ProvisioningState
	var opError *arm.CloudErrorBody

	switch status := breakGlassCredential.Status(); status {
	case cmv1.BreakGlassCredentialStatusCreated:
		opStatus = arm.ProvisioningStateProvisioning
	case cmv1.BreakGlassCredentialStatusFailed:
		// XXX Cluster Service does not provide a reason for the failure,
		//     so we have no choice but to use a generic error message.
		opStatus = arm.ProvisioningStateFailed
		opError = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case cmv1.BreakGlassCredentialStatusIssued:
		opStatus = arm.ProvisioningStateSucceeded
	default:
		s.recordOperationError(ctx, pollBreakGlassCredential, err)
		op.logger.Error(fmt.Sprintf("Unhandled BreakGlassCredentialStatus '%s'", status))
		return
	}

	err = s.patchOperationDocument(ctx, op, opStatus, opError)
	if err != nil {
		s.recordOperationError(ctx, pollBreakGlassCredential, err)
		op.logger.Error(fmt.Sprintf("Failed to update operation status: %v", err))
	}
}

// pollBreakGlassCredentialRevoke updates the status of a credential revocation operation.
func (s *OperationsScanner) pollBreakGlassCredentialRevoke(ctx context.Context, op operation) {
	ctx, span := startChildSpan(ctx, "pollBreakGlassCredentialRevoke")
	defer span.End()
	defer s.updateOperationMetrics(pollBreakGlassCredentialRevoke)()
	op.setSpanAttributes(span)

	var opStatus = arm.ProvisioningStateSucceeded
	var opError *arm.CloudErrorBody

	// XXX Error handling here is tricky. Since the operation applies to multiple
	//     Cluster Service objects, we can find a mix of successes and failures.
	//     And with only a Failed status for each object, it's difficult to make
	//     intelligent decisions like whether to retry. This is just to say the
	//     error handling policy here may need revising once Cluster Service
	//     offers more detail to accompany BreakGlassCredentialStatusFailed.

	iterator := s.clusterService.ListBreakGlassCredentials(op.doc.InternalID, "")

loop:
	for breakGlassCredential := range iterator.Items(ctx) {
		// An expired credential is as good as a revoked credential
		// for this operation, regardless of the credential status.
		if breakGlassCredential.ExpirationTimestamp().After(time.Now()) {
			switch status := breakGlassCredential.Status(); status {
			case cmv1.BreakGlassCredentialStatusAwaitingRevocation:
				opStatus = arm.ProvisioningStateDeleting
				// break alone just breaks out of select.
				// Use a label to break out of the loop.
				break loop
			case cmv1.BreakGlassCredentialStatusRevoked:
				// maintain ProvisioningStateSucceeded
			case cmv1.BreakGlassCredentialStatusFailed:
				// XXX Cluster Service does not provide a reason for the failure,
				//     so we have no choice but to use a generic error message.
				opStatus = arm.ProvisioningStateFailed
				opError = &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInternalServerError,
					Message: "Failed to revoke cluster credential",
				}
				// break alone just breaks out of select.
				// Use a label to break out of the loop.
				break loop
			default:
				err := fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
				s.recordOperationError(ctx, pollBreakGlassCredentialRevoke, err)
				op.logger.Error(err.Error())
			}
		}
	}

	err := iterator.GetError()
	if err != nil {
		s.recordOperationError(ctx, pollBreakGlassCredentialRevoke, err)
		op.logger.Error(fmt.Sprintf("Error while paging through Cluster Service query results: %v", err.Error()))
		return
	}

	err = s.patchOperationDocument(ctx, op, opStatus, opError)
	if err != nil {
		s.recordOperationError(ctx, pollBreakGlassCredentialRevoke, err)
		op.logger.Error(fmt.Sprintf("Failed to update operation status: %v", err))
	}
}

// withSubscriptionLock holds a subscription lock while executing the given function.
// In the event the subscription lock is lost, the context passed to the function will
// be canceled.
func (s *OperationsScanner) withSubscriptionLock(ctx context.Context, logger *slog.Logger, subscriptionID string, fn func(ctx context.Context)) {
	timeout := s.lockClient.GetDefaultTimeToLive()
	lock, err := s.lockClient.AcquireLock(ctx, subscriptionID, &timeout)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to acquire lock: %v", err))
		return
	}

	lockedCtx, stop := s.lockClient.HoldLock(ctx, lock)
	fn(lockedCtx)
	lock = stop()

	if lock != nil {
		nonFatalErr := s.lockClient.ReleaseLock(ctx, lock)
		if nonFatalErr != nil {
			// Failure here is non-fatal but still log the error.
			// The lock's TTL ensures it will be released eventually.
			logger.Warn(fmt.Sprintf("Failed to release lock: %v", nonFatalErr))
		}
	}
}

// setDeleteOperationAsCompleted updates Cosmos DB to reflect a completed resource deletion.
func (s *OperationsScanner) setDeleteOperationAsCompleted(ctx context.Context, op operation) error {
	// Delete the resource document first. If it fails the backend will retry
	// by virtue of the operation document still having a non-terminal status.
	err := s.dbClient.DeleteResourceDoc(ctx, op.doc.ExternalID)
	if err != nil {
		return err
	}

	// Save a final "succeeded" operation status until TTL expires.
	err = s.patchOperationDocument(ctx, op, arm.ProvisioningStateSucceeded, nil)
	if err != nil {
		return err
	}

	return nil
}

// updateOperationStatus updates Cosmos DB to reflect an updated resource status.
func (s *OperationsScanner) updateOperationStatus(ctx context.Context, op operation, opStatus arm.ProvisioningState, opError *arm.CloudErrorBody) error {
	err := s.patchOperationDocument(ctx, op, opStatus, opError)
	if err != nil {
		return err
	}

	var patchOperations database.ResourceDocumentPatchOperations

	scalar := strings.ReplaceAll(database.ResourceDocumentJSONPathActiveOperationID, "/", ".")
	condition := fmt.Sprintf("FROM doc WHERE doc%s = '%s'", scalar, op.id)

	patchOperations.SetCondition(condition)
	patchOperations.SetProvisioningState(opStatus)
	if opStatus.IsTerminal() {
		patchOperations.SetActiveOperationID(nil)
	}

	_, err = s.dbClient.PatchResourceDoc(ctx, op.doc.ExternalID, patchOperations)
	return err
}

// patchOperationDocument patches the status and error fields of an OperationDocument.
func (s *OperationsScanner) patchOperationDocument(ctx context.Context, op operation, opStatus arm.ProvisioningState, opError *arm.CloudErrorBody) error {
	var patchOperations database.OperationDocumentPatchOperations

	scalar := strings.ReplaceAll(database.OperationDocumentJSONPathStatus, "/", ".")
	condition := fmt.Sprintf("FROM doc WHERE doc%s != '%s'", scalar, opStatus)

	patchOperations.SetCondition(condition)
	patchOperations.SetLastTransitionTime(time.Now().UTC())
	patchOperations.SetStatus(opStatus)
	if opError != nil {
		patchOperations.SetError(opError)
	}

	updatedDoc, err := s.dbClient.PatchOperationDoc(ctx, op.pk, op.id, patchOperations)
	if err == nil {
		op.doc = updatedDoc
		message := fmt.Sprintf("Updated status to '%s'", opStatus)
		switch opStatus {
		case arm.ProvisioningStateSucceeded:
			switch op.doc.Request {
			case database.OperationRequestCreate:
				message = "Resource creation succeeded"
			case database.OperationRequestUpdate:
				message = "Resource update succeeded"
			case database.OperationRequestDelete:
				message = "Resource deletion succeeded"
			case database.OperationRequestRequestCredential:
				message = "Credential request succeeded"
			case database.OperationRequestRevokeCredentials:
				message = "Credential revocation succeeded"
			}
		case arm.ProvisioningStateFailed:
			switch op.doc.Request {
			case database.OperationRequestCreate:
				message = "Resource creation failed"
			case database.OperationRequestUpdate:
				message = "Resource update failed"
			case database.OperationRequestDelete:
				message = "Resource deletion failed"
			case database.OperationRequestRequestCredential:
				message = "Credential request failed"
			case database.OperationRequestRevokeCredentials:
				message = "Credential revocation failed"
			}
		}
		op.logger.Info(message)
	} else if !database.IsResponseError(err, http.StatusPreconditionFailed) {
		return err
	}

	if opStatus.IsTerminal() && len(op.doc.NotificationURI) > 0 {
		err = s.postAsyncNotification(ctx, op)
		if err == nil {
			op.logger.Info("Posted async notification")

			// Remove the notification URI from the document
			// so the ARM notification is only sent once.
			var patchOperations database.OperationDocumentPatchOperations
			patchOperations.SetNotificationURI(nil)
			updatedDoc, err = s.dbClient.PatchOperationDoc(ctx, op.pk, op.id, patchOperations)
			if err == nil {
				op.doc = updatedDoc
			} else {
				op.logger.Error(fmt.Sprintf("Failed to clear notification URI: %v", err))
			}
		} else {
			op.logger.Error(fmt.Sprintf("Failed to post async notification: %v", err.Error()))
		}
	}

	return nil
}

// postAsyncNotification submits an POST request with status payload to the given URL.
func (s *OperationsScanner) postAsyncNotification(ctx context.Context, op operation) error {
	data, err := arm.MarshalJSON(op.doc.ToStatus())
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, op.doc.NotificationURI, bytes.NewBuffer(data))
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := s.notificationClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return errors.New(response.Status)
	}

	return nil
}

// convertClusterStatus attempts to translate a ClusterStatus object from
// Cluster Service into an ARM provisioning state and, if necessary, a
// structured OData error.
func (s *OperationsScanner) convertClusterStatus(ctx context.Context, op operation, clusterStatus *arohcpv1alpha1.ClusterStatus) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	var opStatus = op.doc.Status
	var opError *arm.CloudErrorBody
	var err error

	switch state := clusterStatus.State(); state {
	case arohcpv1alpha1.ClusterStateError:
		opStatus = arm.ProvisioningStateFailed
		// Provision error codes are defined in the CS repo:
		// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/cluster_errors.go
		code := clusterStatus.ProvisionErrorCode()
		if code == "" {
			code = arm.CloudErrorCodeInternalServerError
		}
		message := clusterStatus.ProvisionErrorMessage()
		if message == "" {
			message = clusterStatus.Description()
		}
		// Construct the cloud error code depending on the provision error code.
		switch code {
		case InflightChecksFailedProvisionErrorCode:
			opError, err = s.convertInflightChecks(ctx, op.logger, op.doc.InternalID)
			if err != nil {
				return opStatus, opError, err
			}
		default:
			opError = &arm.CloudErrorBody{Code: code, Message: message}
		}
	case arohcpv1alpha1.ClusterStateInstalling:
		opStatus = arm.ProvisioningStateProvisioning
	case arohcpv1alpha1.ClusterStateReady:
		// Resource deletion is successful when fetching its state
		// from Cluster Service returns a "404 Not Found" error. If
		// we see the resource in a "Ready" state during a deletion
		// operation, leave the current provisioning state as is.
		if op.doc.Request != database.OperationRequestDelete {
			opStatus = arm.ProvisioningStateSucceeded
		}
	case arohcpv1alpha1.ClusterStateUninstalling:
		opStatus = arm.ProvisioningStateDeleting
	case arohcpv1alpha1.ClusterStatePending, arohcpv1alpha1.ClusterStateValidating:
		// These are valid cluster states for ARO-HCP but there are
		// no unique ProvisioningState values for them. They should
		// only occur when ProvisioningState is Accepted.
		if opStatus != arm.ProvisioningStateAccepted {
			err = fmt.Errorf("got ClusterState '%s' while ProvisioningState was '%s' instead of '%s'", state, opStatus, arm.ProvisioningStateAccepted)
		}
	default:
		err = fmt.Errorf("unhandled ClusterState '%s'", state)
	}

	return opStatus, opError, err
}

// convertNodePoolStatus attempts to translate a NodePoolStatus object
// from Cluster Service into an ARM provisioning state and, if necessary,
// a structured OData error.
func convertNodePoolStatus(op operation, nodePoolStatus *arohcpv1alpha1.NodePoolStatus) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	var opStatus = op.doc.Status
	var opError *arm.CloudErrorBody
	var err error

	switch state := NodePoolStateValue(nodePoolStatus.State().NodePoolStateValue()); state {
	case NodePoolStateValidating, NodePoolStatePending, NodePoolStateValidatingUpdate, NodePoolStatePendingUpdate:
		// These are valid node pool states for ARO-HCP but there are
		// no unique ProvisioningState values for them. They should
		// only occur when ProvisioningState is Accepted.
		if opStatus != arm.ProvisioningStateAccepted {
			err = fmt.Errorf("got NodePoolStatusValue '%s' while ProvisioningState was '%s' instead of '%s'", state, opStatus, arm.ProvisioningStateAccepted)
		}
	case NodePoolStateInstalling:
		opStatus = arm.ProvisioningStateProvisioning
	case NodePoolStateReady:
		// Resource deletion is successful when fetching its state
		// from Cluster Service returns a "404 Not Found" error. If
		// we see the resource in a "Ready" state during a deletion
		// operation, leave the current provisioning state as is.
		if op.doc.Request != database.OperationRequestDelete {
			opStatus = arm.ProvisioningStateSucceeded
		}
	case NodePoolStateUpdating:
		opStatus = arm.ProvisioningStateUpdating
	case NodePoolStateUninstalling:
		opStatus = arm.ProvisioningStateDeleting
	case NodePoolStateRecoverableError, NodePoolStateError:
		// XXX OCM SDK offers no error code or message for failed node pool
		//     operations so "Internal Server Error" is all we can do for now.
		//     https://issues.redhat.com/browse/ARO-14969
		opStatus = arm.ProvisioningStateFailed
		opError = arm.NewInternalServerError().CloudErrorBody
	default:
		err = fmt.Errorf("unhandled NodePoolState '%s'", state)
	}

	return opStatus, opError, err
}

// convertInflightChecks gets a cluster internal ID, fetches inflight check errors from CS endpoint, and converts them
// to arm.CloudErrorBody type.
// The function should be triggered only if inflight errors occurred with provision error code OCM4001.
func (s *OperationsScanner) convertInflightChecks(ctx context.Context, logger *slog.Logger,
	internalId ocm.InternalID) (*arm.CloudErrorBody, error) {
	inflightChecks, err := s.clusterService.GetClusterInflightChecks(ctx, internalId)
	if err != nil {
		return &arm.CloudErrorBody{}, err
	}

	var cloudErrors []arm.CloudErrorBody
	for _, inflightCheck := range inflightChecks.Items() {
		if inflightCheck.State() == arohcpv1alpha1.InflightCheckStateFailed {
			cloudErrors = append(cloudErrors, convertInflightCheck(inflightCheck, logger))
		}
	}

	// This is a fallback case and should not normally occur. If the provision error code is OCM4001,
	// there should be at least one inflight failure.
	if len(cloudErrors) == 0 {
		logger.Error(fmt.Sprintf(
			"Cluster '%s' returned error code OCM4001, but no inflight failures were found", internalId))
		return &arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInternalServerError,
		}, nil
	}

	if len(cloudErrors) == 1 {
		return &arm.CloudErrorBody{
			Code:    cloudErrors[0].Code,
			Message: cloudErrors[0].Message,
		}, nil
	}

	return &arm.CloudErrorBody{
		Code:    arm.CloudErrorCodeMultipleErrorsOccurred,
		Message: "Content validation failed on multiple fields",
		Details: cloudErrors,
	}, nil
}

func convertInflightCheck(inflightCheck *arohcpv1alpha1.InflightCheck, logger *slog.Logger) arm.CloudErrorBody {
	message, succeeded := convertInflightCheckDetails(inflightCheck)
	if !succeeded {
		logger.Error(fmt.Sprintf("error converting inflight check '%s' details", inflightCheck.Name()))
	}

	return arm.CloudErrorBody{
		Code:    arm.CloudErrorCodeInternalServerError,
		Message: message,
	}
}

// convertInflightCheckDetails gets an inflight check object and extracts the error message.
func convertInflightCheckDetails(inflightCheck *arohcpv1alpha1.InflightCheck) (string, bool) {
	details, ok := inflightCheck.GetDetails()
	if !ok {
		return "", false
	}

	detailsMap, ok := details.(map[string]interface{})
	if !ok {
		return "", false
	}

	// Retrieve "error" key safely
	if errMsg, exists := detailsMap["error"]; exists {
		if errStr, ok := errMsg.(string); ok {
			return errStr, true
		}
	}

	return "", false
}

// startRootSpan initiates a new parent trace.
func startRootSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.GetTracerProvider().
		Tracer(tracerName).
		Start(
			ctx,
			name,
			trace.WithNewRoot(),
			trace.WithSpanKind(trace.SpanKindInternal),
		)
}

// startChildSpan creates a new span linked to the parent span from the current context.
func startChildSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return trace.SpanFromContext(ctx).
		TracerProvider().
		Tracer(tracerName).
		Start(ctx, name)
}
