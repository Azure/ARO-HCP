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

// Package app wires the kube-applier binary together. It is invoked from
// kube-applier/cmd after flags have been parsed and external dependencies
// (kubeconfig, leader-election lock, Cosmos client) have been constructed.
package app

import (
	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/Azure/ARO-HCP/internal/database"
)

// AppShortDescriptionName is the human-readable identity used in startup logs
// and OpenTelemetry resource attributes.
const AppShortDescriptionName = "ARO HCP kube-applier"

// Per-controller worker counts. Hardcoded because the workload is bounded
// (low thousands of *Desires per management cluster) and operators have no
// reason to tune these per-environment. Read-manager threading is 1 because
// its work is bookkeeping; the per-instance kube reflectors run in their
// own goroutines independent of this count.
const (
	threadsApply       = 4
	threadsDelete      = 4
	threadsReadManager = 1
)

// Options is the wired bundle of dependencies the kube-applier needs to run.
// All fields are required unless noted.
type Options struct {
	// ManagementCluster is this pod's management cluster name. It is the
	// Cosmos partition key for every *Desire the binary reads or writes.
	ManagementCluster string

	LeaderElectionLock  resourcelock.Interface
	KubeApplierDBClient database.KubeApplierDBClient
	DynamicClient       dynamic.Interface

	MetricsServerListenAddress string
	HealthzServerListenAddress string

	// MetricsRegisterer / MetricsGatherer are optional overrides for tests.
	// Production wiring uses component-base's legacyregistry.
	MetricsRegisterer prometheus.Registerer
	MetricsGatherer   prometheus.Gatherer

	ExitOnPanic bool
}
