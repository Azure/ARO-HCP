package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	defaultSubscriptionConcurrency   = 10
	defaultPollIntervalSubscriptions = 10 * time.Minute
	defaultPollIntervalOperations    = 10 * time.Second

	collectSubscriptionsLabel  = "list_subscriptions"
	processSubscriptionsLabel  = "process_subscriptions"
	processOperationsLabel     = "process_operations"
	pollClusterOperationLabel  = "poll_cluster"
	pollNodePoolOperationLabel = "poll_node_pool"
)

type operation struct {
	id     string
	pk     azcosmos.PartitionKey
	doc    *database.OperationDocument
	logger *slog.Logger
}

type OperationsScanner struct {
	dbClient            database.DBClient
	lockClient          *database.LockClient
	clusterService      ocm.ClusterServiceClient
	notificationClient  *http.Client
	subscriptions       []string
	subscriptionsLock   sync.Mutex
	subscriptionChannel chan string
	subscriptionWorkers sync.WaitGroup

	leaderGauge            prometheus.Gauge
	workerGauge            prometheus.Gauge
	operationsCount        *prometheus.CounterVec
	operationsFailedCount  *prometheus.CounterVec
	operationsDuration     *prometheus.HistogramVec
	lastOperationTimestamp *prometheus.GaugeVec
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
	}

	// Initialize the counter and histogram metrics.
	for _, v := range []string{
		collectSubscriptionsLabel,
		processSubscriptionsLabel,
		processOperationsLabel,
		pollClusterOperationLabel,
		pollNodePoolOperationLabel,
	} {
		s.operationsCount.WithLabelValues(v)
		s.operationsFailedCount.WithLabelValues(v)
		s.operationsDuration.WithLabelValues(v)
		s.lastOperationTimestamp.WithLabelValues(v)
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
			s.processSubscriptions(logger)
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
	defer s.updateOperationMetrics(collectSubscriptionsLabel)()

	var subscriptions []string

	iterator := s.dbClient.ListAllSubscriptionDocs()

	for subscriptionID, subscription := range iterator.Items(ctx) {
		// Unregistered subscriptions should have no active operations,
		// not even deletes.
		if subscription.State != arm.SubscriptionStateUnregistered {
			subscriptions = append(subscriptions, subscriptionID)
		}
	}

	err := iterator.GetError()
	if err != nil {
		s.operationsFailedCount.WithLabelValues(collectSubscriptionsLabel).Inc()
		logger.Error(fmt.Sprintf("Error while paging through Cosmos query results: %v", err.Error()))
		return
	}

	s.subscriptionsLock.Lock()
	defer s.subscriptionsLock.Unlock()

	if len(subscriptions) != len(s.subscriptions) {
		logger.Info(fmt.Sprintf("Tracking %d active subscriptions", len(subscriptions)))
	}

	s.subscriptions = subscriptions
}

// processSubscriptions feeds the internal list of Azure subscription IDs
// to the worker pool for processing. processSubscriptions may block if the
// worker pool gets overloaded. The log will indicate if this occurs.
func (s *OperationsScanner) processSubscriptions(logger *slog.Logger) {
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
	defer s.updateOperationMetrics(processOperationsLabel)()

	var numProcessed int

	pk := database.NewPartitionKey(subscriptionID)

	iterator := s.dbClient.ListOperationDocs(pk)

	for operationID, operationDoc := range iterator.Items(ctx) {
		if !operationDoc.Status.IsTerminal() {
			operationLogger := logger.With(
				"operation", operationDoc.Request,
				"operation_id", operationID,
				"resource_id", operationDoc.ExternalID.String(),
				"internal_id", operationDoc.InternalID.String())
			op := operation{operationID, pk, operationDoc, operationLogger}

			switch operationDoc.InternalID.Kind() {
			case cmv1.ClusterKind:
				s.pollClusterOperation(ctx, op)
				numProcessed++
			case cmv1.NodePoolKind:
				s.pollNodePoolOperation(ctx, op)
				numProcessed++
			}
		}
	}

	err := iterator.GetError()
	if err != nil {
		s.operationsFailedCount.WithLabelValues(processOperationsLabel).Inc()
		logger.Error(fmt.Sprintf("Error while paging through Cosmos query results: %v", err.Error()))
	}
}

// pollClusterOperation updates the status of a cluster operation.
func (s *OperationsScanner) pollClusterOperation(ctx context.Context, op operation) {
	defer s.updateOperationMetrics(pollClusterOperationLabel)()

	clusterStatus, err := s.clusterService.GetClusterStatus(ctx, op.doc.InternalID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound && op.doc.Request == database.OperationRequestDelete {
			err = s.setDeleteOperationAsCompleted(ctx, op)
			if err != nil {
				op.logger.Error(fmt.Sprintf("Failed to handle a completed deletion: %v", err))
			}
		} else {
			op.logger.Error(fmt.Sprintf("Failed to get cluster status: %v", err))
		}

		s.operationsFailedCount.WithLabelValues(pollClusterOperationLabel).Inc()
		return
	}

	opStatus, opError, err := convertClusterStatus(clusterStatus, op.doc.Status)
	if err != nil {
		s.operationsFailedCount.WithLabelValues(pollClusterOperationLabel).Inc()
		op.logger.Warn(err.Error())
		return
	}

	err = s.updateOperationStatus(ctx, op, opStatus, opError)
	if err != nil {
		s.operationsFailedCount.WithLabelValues(pollClusterOperationLabel).Inc()
		op.logger.Error(fmt.Sprintf("Failed to update operation status: %v", err))
	}
}

// pollNodePoolOperation updates the status of a node pool operation.
func (s *OperationsScanner) pollNodePoolOperation(ctx context.Context, op operation) {
	defer s.updateOperationMetrics(pollNodePoolOperationLabel)()

	_, err := s.clusterService.GetNodePool(ctx, op.doc.InternalID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound && op.doc.Request == database.OperationRequestDelete {
			err = s.setDeleteOperationAsCompleted(ctx, op)
			if err != nil {
				op.logger.Error(fmt.Sprintf("Failed to handle a completed deletion: %v", err))
			}
		} else {
			op.logger.Error(fmt.Sprintf("Failed to get node pool status: %v", err))
		}
		return
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
	err := s.dbClient.DeleteResourceDoc(ctx, op.doc.ExternalID)
	if err != nil {
		return err
	}

	// Save a final "succeeded" operation status until TTL expires.
	const opStatus arm.ProvisioningState = arm.ProvisioningStateSucceeded
	updated, err := s.dbClient.UpdateOperationDoc(ctx, op.pk, op.id, func(updateDoc *database.OperationDocument) bool {
		return updateDoc.UpdateStatus(opStatus, nil)
	})
	if err != nil {
		return err
	}
	if updated {
		op.logger.Info("Deletion completed")
		s.maybePostAsyncNotification(ctx, op)
	}

	return nil
}

// updateOperationStatus updates Cosmos DB to reflect an updated resource status.
func (s *OperationsScanner) updateOperationStatus(ctx context.Context, op operation, opStatus arm.ProvisioningState, opError *arm.CloudErrorBody) error {
	updated, err := s.dbClient.UpdateOperationDoc(ctx, op.pk, op.id, func(updateDoc *database.OperationDocument) bool {
		return updateDoc.UpdateStatus(opStatus, opError)
	})
	if err != nil {
		return err
	}
	if updated {
		op.logger.Info(fmt.Sprintf("Updated status to '%s'", opStatus))
		s.maybePostAsyncNotification(ctx, op)
	}

	_, err = s.dbClient.UpdateResourceDoc(ctx, op.doc.ExternalID, func(updateDoc *database.ResourceDocument) bool {
		var updated bool

		if op.id == updateDoc.ActiveOperationID {
			if opStatus != updateDoc.ProvisioningState {
				updateDoc.ProvisioningState = opStatus
				updated = true
			}
			if opStatus.IsTerminal() {
				updateDoc.ActiveOperationID = ""
				updated = true
			}
		}

		return updated
	})
	if err != nil {
		return err
	}

	return nil
}

// maybePostAsyncNotification attempts to notify ARM of a completed asynchronous
// operation if the initial request included an "Azure-AsyncNotificationUri" header.
func (s *OperationsScanner) maybePostAsyncNotification(ctx context.Context, op operation) {
	if len(op.doc.NotificationURI) > 0 {
		err := s.postAsyncNotification(ctx, op)
		if err == nil {
			op.logger.Info("Posted async notification")
		} else {
			op.logger.Error(fmt.Sprintf("Failed to post async notification: %v", err.Error()))
		}
	}
}

// postAsyncNotification submits an POST request with status payload to the given URL.
func (s *OperationsScanner) postAsyncNotification(ctx context.Context, op operation) error {
	// Refetch the operation document to provide the latest status.
	doc, err := s.dbClient.GetOperationDoc(ctx, op.pk, op.id)
	if err != nil {
		return err
	}

	data, err := arm.Marshal(doc.ToStatus())
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.NotificationURI, bytes.NewBuffer(data))
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
func convertClusterStatus(clusterStatus *arohcpv1alpha1.ClusterStatus, current arm.ProvisioningState) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	var opStatus arm.ProvisioningState = current
	var opError *arm.CloudErrorBody
	var err error

	// FIXME This logic is all tenative until the new "/api/aro_hcp/v1" OCM
	//       API is available. What's here now is a best guess at converting
	//       ClusterStatus from the "/api/clusters_mgmt/v1" API.

	switch state := clusterStatus.State(); state {
	case arohcpv1alpha1.ClusterStateError:
		opStatus = arm.ProvisioningStateFailed
		// FIXME This is guesswork. Need clarity from Cluster Service
		//       on what provision error codes are possible so we can
		//       translate to an appropriate cloud error code.
		code := clusterStatus.ProvisionErrorCode()
		if code == "" {
			code = arm.CloudErrorCodeInternalServerError
		}
		message := clusterStatus.ProvisionErrorMessage()
		if message == "" {
			message = clusterStatus.Description()
		}
		opError = &arm.CloudErrorBody{Code: code, Message: message}
	case arohcpv1alpha1.ClusterStateInstalling:
		opStatus = arm.ProvisioningStateProvisioning
	case arohcpv1alpha1.ClusterStateReady:
		opStatus = arm.ProvisioningStateSucceeded
	case arohcpv1alpha1.ClusterStateUninstalling:
		opStatus = arm.ProvisioningStateDeleting
	case arohcpv1alpha1.ClusterStatePending, arohcpv1alpha1.ClusterStateValidating:
		// These are valid cluster states for ARO-HCP but there are
		// no unique ProvisioningState values for them. They should
		// only occur when ProvisioningState is Accepted.
		if current != arm.ProvisioningStateAccepted {
			err = fmt.Errorf("Got ClusterState '%s' while ProvisioningState was '%s' instead of '%s'", state, current, arm.ProvisioningStateAccepted)
		}
	default:
		err = fmt.Errorf("Unhandled ClusterState '%s'", state)
	}

	return opStatus, opError, err
}
