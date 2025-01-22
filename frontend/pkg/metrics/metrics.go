package metrics

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Azure/ARO-HCP/internal/database"
)

type subscription struct {
	id         string
	state      string
	updated_at int64
}

type SubscriptionCollector struct {
	dbClient database.DBClient
	location string

	errCounter               prometheus.Counter
	refreshCounter           prometheus.Counter
	lastSyncDuration         prometheus.Gauge
	lastSyncResult           prometheus.Gauge
	lastSuccessSyncTimestamp prometheus.Gauge

	mtx           sync.RWMutex
	subscriptions map[string]subscription
}

const (
	errCounterName               = "frontend_subscription_collector_failed_syncs_total"
	refreshCounterName           = "frontend_subscription_collector_syncs_total"
	lastSyncDurationName         = "frontend_subscription_collector_last_sync_duration_seconds"
	lastSyncResultName           = "frontend_subscription_collector_last_sync"
	lastSuccessSyncTimestampName = "frontend_subscription_collector_last_success_timestamp_seconds"
	subscriptionStateName        = "frontend_lifecycle_state"
	subscriptionLastUpdatedName  = "frontend_lifecycle_last_update_timestamp_seconds"
)

func NewSubscriptionCollector(r prometheus.Registerer, dbClient database.DBClient, location string) *SubscriptionCollector {
	sc := &SubscriptionCollector{
		dbClient: dbClient,
		location: location,

		errCounter: promauto.With(r).NewCounter(
			prometheus.CounterOpts{
				Name: errCounterName,
				Help: "Total number of failed syncs for the Subscription collector.",
			},
		),
		refreshCounter: promauto.With(r).NewCounter(
			prometheus.CounterOpts{
				Name: refreshCounterName,
				Help: "Total number of syncs for the Subscription collector.",
			},
		),
		lastSyncDuration: promauto.With(r).NewGauge(
			prometheus.GaugeOpts{
				Name: lastSyncDurationName,
				Help: "Last sync operation's duration.",
			},
		),
		lastSyncResult: promauto.With(r).NewGauge(
			prometheus.GaugeOpts{
				Name: lastSyncResultName,
				Help: "Last sync operation's result (1: success, 0: failed).",
			},
		),
		lastSuccessSyncTimestamp: promauto.With(r).NewGauge(
			prometheus.GaugeOpts{
				Name: lastSuccessSyncTimestampName,
				Help: "Last successful operation's timestamp.",
			},
		),
	}
	// Register the collector itself.
	r.MustRegister(sc)

	return sc
}

// Run starts the loop which reads the subscriptions from the database at
// periodic intervals (30s) to populate the subscription metrics.
func (sc *SubscriptionCollector) Run(logger *slog.Logger, stop <-chan struct{}) {
	// Populate the internal cache.
	sc.refresh(logger)

	t := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			sc.refresh(logger)
		}
	}
}

func (sc *SubscriptionCollector) refresh(logger *slog.Logger) {
	now := time.Now()
	defer func() {
		sc.lastSyncDuration.Set(time.Since(now).Seconds())
	}()

	sc.refreshCounter.Inc()
	if err := sc.updateCache(); err != nil {
		logger.Warn("failed to update subscription collector cache", "err", err)
		sc.lastSyncResult.Set(0)
		sc.errCounter.Inc()
		return
	}

	sc.lastSyncResult.Set(1)
	sc.lastSuccessSyncTimestamp.SetToCurrentTime()
}

func (sc *SubscriptionCollector) updateCache() error {
	subscriptions := make(map[string]subscription)

	iter := sc.dbClient.ListAllSubscriptionDocs()
	for id, sub := range iter.Items(context.Background()) {
		subscriptions[id] = subscription{
			id:         id,
			state:      string(sub.State),
			updated_at: int64(sub.LastUpdated),
		}
	}
	if err := iter.GetError(); err != nil {
		return err
	}

	sc.mtx.Lock()
	sc.subscriptions = subscriptions
	sc.mtx.Unlock()

	return nil
}

// GetSubscriptionState returns the state of the subscription.
func (sc *SubscriptionCollector) GetSubscriptionState(id string) string {
	sc.mtx.RLock()
	defer sc.mtx.RUnlock()

	if s, found := sc.subscriptions[id]; found {
		return s.state
	}

	return "Unknown"
}

var (
	subscriptionStateDesc = prometheus.NewDesc(
		subscriptionStateName,
		"Reports the current state of the subscription.",
		[]string{"location", "subscription_id", "state"},
		nil,
	)
	subscriptionLastUpdatedDesc = prometheus.NewDesc(
		subscriptionLastUpdatedName,
		"Reports the timestamp when the subscription has been updated for the last time.",
		[]string{"location", "subscription_id"},
		nil,
	)
)

// Describe implements the prometheus.Collector interface.
func (sc *SubscriptionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- subscriptionStateDesc
	ch <- subscriptionLastUpdatedDesc
}

// Collect implements the prometheus.Collector interface.
func (sc *SubscriptionCollector) Collect(ch chan<- prometheus.Metric) {
	sc.mtx.RLock()
	defer sc.mtx.RUnlock()

	for _, sub := range sc.subscriptions {
		ch <- prometheus.MustNewConstMetric(
			subscriptionStateDesc,
			prometheus.GaugeValue,
			1.0,
			sc.location,
			sub.id,
			string(sub.state),
		)
		ch <- prometheus.MustNewConstMetric(
			subscriptionLastUpdatedDesc,
			prometheus.GaugeValue,
			float64(sub.updated_at),
			sc.location,
			sub.id,
		)
	}
}
