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

package oldoperationscanner

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	backendtracing "github.com/Azure/ARO-HCP/backend/pkg/tracing"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/utils"
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
	pollExternalAuthOperationLabel = "poll_external_auth"
)

type operation struct {
	id  string
	doc *api.Operation
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
		pollExternalAuthOperationLabel,
	})
}

type OperationsScanner struct {
	dbClient           database.DBClient
	lockClient         database.LockClientInterface
	clusterService     ocm.ClusterServiceClientSpec
	azureLocation      string
	notificationClient *http.Client

	subscriptionsLister listers.SubscriptionLister
	subscriptionsLock   sync.Mutex
	subscriptions       []string
	subscriptionChannel chan string
	subscriptionWorkers sync.WaitGroup

	// Allow overriding timestamps for testing.
	newTimestamp func() time.Time

	LeaderGauge            prometheus.Gauge
	workerGauge            prometheus.Gauge
	operationsCount        *prometheus.CounterVec
	operationsFailedCount  *prometheus.CounterVec
	operationsDuration     *prometheus.HistogramVec
	lastOperationTimestamp *prometheus.GaugeVec
	subscriptionsByState   *prometheus.GaugeVec
}

func NewOperationsScanner(dbClient database.DBClient, clustersServiceClient ocm.ClusterServiceClientSpec, azureLocation string, subscriptionLister listers.SubscriptionLister) *OperationsScanner {
	s := &OperationsScanner{
		dbClient:            dbClient,
		lockClient:          dbClient.GetLockClient(),
		clusterService:      clustersServiceClient,
		azureLocation:       azureLocation,
		notificationClient:  http.DefaultClient,
		subscriptionsLister: subscriptionLister,
		subscriptions:       make([]string, 0),

		newTimestamp: func() time.Time { return time.Now().UTC() },

		LeaderGauge: promauto.With(prometheus.DefaultRegisterer).NewGauge(
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
func getInterval(ctx context.Context, envName string, defaultVal time.Duration) time.Duration {
	logger := utils.LoggerFromContext(ctx)

	if intervalString, ok := os.LookupEnv(envName); ok {
		interval, err := time.ParseDuration(intervalString)
		if err == nil {
			return interval
		} else {
			logger.Error(err, "Cannot use environment variable", "envName", envName)
		}
	}
	return defaultVal
}

// getPositiveInt parses an environment variable into a positive integer.
// If the environment variable is not defined or its value is invalid,
// getPositiveInt returns defaultVal.
func getPositiveInt(ctx context.Context, envName string, defaultVal int) int {
	logger := utils.LoggerFromContext(ctx)

	if intString, ok := os.LookupEnv(envName); ok {
		positiveInt, err := strconv.Atoi(intString)
		if err == nil && positiveInt <= 0 {
			err = errors.New("value must be positive")
		}

		if err == nil {
			return positiveInt
		}

		logger.Error(err, "Cannot use environment variable", "envName", envName)
	}

	return defaultVal
}

// Run executes the main loop of the OperationsScanner.
func (s *OperationsScanner) Run(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	if len(s.azureLocation) == 0 {
		panic("azureLocation must be set")
	}

	var interval time.Duration

	interval = getInterval(ctx, "BACKEND_POLL_INTERVAL_SUBSCRIPTIONS", defaultPollIntervalSubscriptions)
	logger.Info("Polling subscriptions in Cosmos DB every " + interval.String())
	collectSubscriptionsTicker := time.NewTicker(interval)

	interval = getInterval(ctx, "BACKEND_POLL_INTERVAL_OPERATIONS", defaultPollIntervalOperations)
	logger.Info("Polling operations in Cosmos DB every " + interval.String())
	processSubscriptionsTicker := time.NewTicker(interval)

	numWorkers := getPositiveInt(ctx, "BACKEND_SUBSCRIPTION_CONCURRENCY", defaultSubscriptionConcurrency)
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
				localCtx, span := StartRootSpan(ctx, "processOperations")
				span.SetAttributes(tracing.SubscriptionIDKey.String(subscriptionID))

				localLogger := logger.WithValues(utils.LogValues{}.AddSubscriptionID(subscriptionID)...)
				localCtx = utils.ContextWithLogger(localCtx, localLogger)
				s.withSubscriptionLock(localCtx, subscriptionID, func(ctx context.Context) {
					s.processOperations(localCtx, subscriptionID)
				})

				span.End()
			}
		}()
	}
	s.subscriptionWorkers.Add(numWorkers)

	// Collect subscriptions immediately on startup.
	s.collectSubscriptions(ctx)

loop:
	for {
		select {
		case <-collectSubscriptionsTicker.C:
			s.collectSubscriptions(ctx)
		case <-processSubscriptionsTicker.C:
			s.processSubscriptions(ctx)
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
	s.LeaderGauge.Set(0)
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
func (s *OperationsScanner) collectSubscriptions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	ctx, span := StartRootSpan(ctx, "collectSubscriptions")
	defer span.End()
	defer s.updateOperationMetrics(collectSubscriptionsLabel)()

	var subscriptions []string

	iterator, err := s.dbClient.Subscriptions().List(ctx, nil)
	if err != nil {
		s.recordOperationError(ctx, collectSubscriptionsLabel, err)
		logger.Error(err, "Error creating iterator")
		return
	}

	subscriptionStates := map[arm.SubscriptionState]int{}
	for subscriptionState := range arm.ListSubscriptionStates() {
		subscriptionStates[subscriptionState] = 0
	}
	for _, subscription := range iterator.Items(ctx) {
		// Unregistered subscriptions should have no active operations,
		// not even deletes.
		if subscription.State != arm.SubscriptionStateUnregistered {
			subscriptions = append(subscriptions, subscription.ResourceID.SubscriptionID)
		}
		subscriptionStates[subscription.State]++
	}

	span.SetAttributes(tracing.ProcessedItemsKey.Int(len(subscriptions)))
	if err := iterator.GetError(); err != nil {
		s.recordOperationError(ctx, collectSubscriptionsLabel, err)
		logger.Error(err, "Error while paging through Cosmos query results")
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
func (s *OperationsScanner) processSubscriptions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	_, span := StartRootSpan(ctx, "processSubscriptions")
	defer span.End()
	defer s.updateOperationMetrics(processSubscriptionsLabel)()

	// This method may block while feeding subscription IDs to the worker pool
	subscriptions, err := s.subscriptionsLister.List(ctx)
	if err != nil {
		logger.Error(err, "Failed to get subscriptions")
		// primitive avoidance of hot loop during refactor.
		time.Sleep(100 * time.Millisecond)
		return
	}

	for _, subscription := range subscriptions {
		select {
		case s.subscriptionChannel <- subscription.ResourceID.SubscriptionID:
		default:
			// The channel is full. Push the subscription anyway
			// but log how long we block for. This will indicate
			// when the worker pool size needs increased.
			start := time.Now()
			s.subscriptionChannel <- subscription.ResourceID.SubscriptionID
			logger.Info("Subscription processing blocked", "duration", time.Since(start).String(), "subscription", subscription.ResourceID.SubscriptionID)
		}
	}
}

// processOperations processes all operations in a single Azure subscription.
func (s *OperationsScanner) processOperations(ctx context.Context, subscriptionID string) {
	logger := utils.LoggerFromContext(ctx)
	defer s.updateOperationMetrics(processOperationsLabel)()

	iterator := s.dbClient.Operations(subscriptionID).ListActiveOperations(nil)

	var n int
	for operationID, operationDoc := range iterator.Items(ctx) {
		n++

		// add info for our logger
		localLogger := logger.WithValues(
			utils.LogValues{}.
				AddOperation(string(operationDoc.Request)).
				AddOperationID(operationID).
				AddLogValuesForResourceID(operationDoc.ExternalID).
				AddInternalID(operationDoc.InternalID.String()).
				AddClientRequestID(operationDoc.ClientRequestID).
				AddCorrelationRequestID(operationDoc.CorrelationRequestID)...)
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		s.processOperation(
			localCtx,
			operation{
				id:  operationID,
				doc: operationDoc,
			})
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(tracing.ProcessedItemsKey.Int(n))

	err := iterator.GetError()
	if err != nil {
		s.recordOperationError(ctx, processOperationsLabel, err)
		logger.Error(err, "Error while paging through Cosmos query results")
	}
}

// processOperation processes a single operation on a resource.
func (s *OperationsScanner) processOperation(ctx context.Context, op operation) {
	logger := utils.LoggerFromContext(ctx)
	_, span := startChildSpan(ctx, "processOperation")
	defer span.End()

	logger.Info("Processing")
	defer logger.Info("Processed")

	// XXX The previous business logic of OperationsScanner has
	//     been converted to various Kubernetes-style controllers
	//     that fulfill the OperationSynchronizer interface.
}

func (s *OperationsScanner) recordOperationError(ctx context.Context, operationName string, err error) {
	if err == nil {
		return
	}

	s.operationsFailedCount.WithLabelValues(operationName).Inc()
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// withSubscriptionLock holds a subscription lock while executing the given function.
// In the event the subscription lock is lost, the context passed to the function will
// be canceled.
func (s *OperationsScanner) withSubscriptionLock(ctx context.Context, subscriptionID string, fn func(ctx context.Context)) {
	WithSubscriptionLock(ctx, s.lockClient, subscriptionID, fn)
}

func WithSubscriptionLock(ctx context.Context, lockClient database.LockClientInterface, subscriptionID string, fn func(ctx context.Context)) {
	logger := utils.LoggerFromContext(ctx)

	timeout := lockClient.GetDefaultTimeToLive()
	span := trace.SpanFromContext(ctx)
	lock, err := lockClient.AcquireLock(ctx, subscriptionID, &timeout)
	if err != nil {
		logger.Error(err, "Failed to acquire lock")
		span.RecordError(err)
		return
	}
	logger.Info("Acquired lock")

	lockedCtx, stop := lockClient.HoldLock(ctx, lock)
	fn(lockedCtx)
	lock = stop()

	if lock != nil {
		nonFatalErr := lockClient.ReleaseLock(ctx, lock)
		if nonFatalErr == nil {
			logger.Info("Released lock")
		} else {
			// Failure here is non-fatal but still log the error.
			// The lock's TTL ensures it will be released eventually.
			logger.Error(nonFatalErr, "Failed to release lock")
			span.RecordError(nonFatalErr)
		}
	}
}

// PostAsyncNotification submits an POST request with status payload to the given URL.
func (s *OperationsScanner) postAsyncNotification(ctx context.Context, operation *api.Operation) error {
	return operationcontrollers.PostAsyncNotification(ctx, s.notificationClient, operation)
}

// StartRootSpan initiates a new parent trace.
func StartRootSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.GetTracerProvider().
		Tracer(backendtracing.BackendTracerName).
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
		Tracer(backendtracing.BackendTracerName).
		Start(ctx, name)
}
