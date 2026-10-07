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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
)

// Metrics for the alert processing rule reaper. The observed gauge is the one to
// alert on: Azure caps a subscription at 1000 alert processing rules, so a gauge
// trending towards that ceiling means the reaper is not keeping up (or rules are
// being created faster than they expire).
var (
	observedRules = promauto.With(legacyregistry.Registerer()).NewGauge(
		prometheus.GaugeOpts{
			Name: "fleet_alert_processing_rules_observed",
			Help: "Number of alert processing rules present in the managed resource group at the last reaping pass.",
		},
	)

	expiredRules = promauto.With(legacyregistry.Registerer()).NewGauge(
		prometheus.GaugeOpts{
			Name: "fleet_alert_processing_rules_expired",
			Help: "Number of alert processing rules found eligible for reaping at the last reaping pass.",
		},
	)

	deletedRulesTotal = promauto.With(legacyregistry.Registerer()).NewCounter(
		prometheus.CounterOpts{
			Name: "fleet_alert_processing_rules_deleted_total",
			Help: "Total number of expired alert processing rules deleted, including those already removed by a concurrent operator action.",
		},
	)

	deleteErrorsTotal = promauto.With(legacyregistry.Registerer()).NewCounter(
		prometheus.CounterOpts{
			Name: "fleet_alert_processing_rules_delete_errors_total",
			Help: "Total number of failed attempts to delete an expired alert processing rule.",
		},
	)

	listErrorsTotal = promauto.With(legacyregistry.Registerer()).NewCounter(
		prometheus.CounterOpts{
			Name: "fleet_alert_processing_rules_list_errors_total",
			Help: "Total number of failed attempts to list alert processing rules in the managed resource group.",
		},
	)
)
