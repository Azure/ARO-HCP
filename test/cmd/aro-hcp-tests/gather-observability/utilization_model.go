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

package gatherobservability

import "time"

const utilizationSchemaVersion = 1

// utilizationReport is the persisted replay contract. Nil measurements mean
// unknown, not zero. CPU quantities are cores and memory quantities are bytes.
type utilizationReport struct {
	SchemaVersion int                   `json:"schemaVersion"`
	GeneratedAt   time.Time             `json:"generatedAt"`
	Start         time.Time             `json:"start"`
	End           time.Time             `json:"end"`
	CPUWindow     string                `json:"cpuWindow"`
	MemoryWindow  string                `json:"memoryWindow"`
	Step          string                `json:"step"`
	Clusters      []string              `json:"clusters"`
	Warnings      []string              `json:"warnings,omitempty"`
	Snapshots     []utilizationSnapshot `json:"snapshots"`
	// Optional for replay of older reports which retained only excluded counts.
	Coverage []utilizationCoverage `json:"coverage,omitempty"`
	// Absent in older peak-only artifacts. History retains the evaluated grid,
	// including empty minutes, so replay never interpolates missing telemetry.
	History []utilizationHistorySample `json:"history,omitempty"`
}

type utilizationHistorySample struct {
	Time     time.Time                 `json:"time"`
	Expected []string                  `json:"expected"`
	Nodes    []utilizationHistoryEntry `json:"nodes"`
	Warnings []string                  `json:"warnings,omitempty"`
}

type utilizationHistoryResources struct {
	CPU      *float64 `json:"cpu"`
	Memory   *float64 `json:"memory"`
	SwiftNIC *float64 `json:"swiftNIC"`
}

type utilizationHistoryEntry struct {
	Cluster         string                      `json:"cluster"`
	Name            string                      `json:"name"`
	Pool            string                      `json:"pool"`
	SKU             string                      `json:"sku"`
	Inventory       bool                        `json:"inventory"`
	SwiftAdvertised *bool                       `json:"swiftAdvertised"`
	Capacity        utilizationHistoryResources `json:"capacity"`
	Allocatable     utilizationHistoryResources `json:"allocatable"`
	Usage           utilizationHistoryResources `json:"usage"`
	Requests        utilizationHistoryResources `json:"requests"`
}

// Intervals contain inclusive UTC minute samples. Adjacent samples with the
// same eligibility and diagnostics are coalesced, not interpolated.
type utilizationCoverage struct {
	Scope     string                        `json:"scope"`
	Resource  string                        `json:"resource"`
	Intervals []utilizationCoverageInterval `json:"intervals"`
}

type utilizationCoverageInterval struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Eligible bool      `json:"eligible"`
	// Counts describe the sample at each minute, not a sum across the interval.
	Nodes            int `json:"nodes"`
	MissingInventory int `json:"missingInventory,omitempty"`
	MissingUsage     int `json:"missingUsage,omitempty"`
	MissingCapacity  int `json:"missingCapacity,omitempty"`
	MissingClusters  int `json:"missingClusters,omitempty"`
}

type utilizationSnapshot struct {
	Time      time.Time             `json:"time"`
	Reasons   []string              `json:"reasons"`
	Warnings  []string              `json:"warnings,omitempty"`
	Nodes     []utilizationNode     `json:"nodes"`
	Workloads []utilizationWorkload `json:"workloads"`
}

type utilizationResources struct {
	CPU    *float64 `json:"cpu"`
	Memory *float64 `json:"memory"`
}

type utilizationNode struct {
	Cluster     string               `json:"cluster"`
	Name        string               `json:"name"`
	Pool        string               `json:"pool"`
	SKU         string               `json:"sku"`
	Capacity    utilizationResources `json:"capacity"`
	Allocatable utilizationResources `json:"allocatable"`
	Usage       utilizationResources `json:"usage"`
}

// Rows retain node placement but aggregate replicas, never individual pod
// identities. Empty Node with Unscheduled=true is pending demand, not a pool.
type utilizationWorkload struct {
	Cluster     string                 `json:"cluster"`
	Namespace   string                 `json:"namespace"`
	Kind        string                 `json:"kind"`
	Name        string                 `json:"name"`
	Component   string                 `json:"component"`
	Node        string                 `json:"node"`
	Unscheduled bool                   `json:"unscheduled"`
	Pods        int                    `json:"pods"`
	PendingPods int                    `json:"pendingPods"`
	Usage       utilizationResources   `json:"usage"`
	Requests    utilizationResources   `json:"requests"`
	Limits      utilizationResources   `json:"limits"`
	Containers  []utilizationContainer `json:"containers"`
}

type utilizationContainer struct {
	Name     string               `json:"name"`
	Usage    utilizationResources `json:"usage"`
	Requests utilizationResources `json:"requests"`
	Limits   utilizationResources `json:"limits"`
	// Limits are finite sums; confirmed unlimited containers contribute zero.
	// Nil counts mean limit coverage is unknown, not that no containers are unlimited.
	UnlimitedCPU    *int `json:"unlimitedCPU"`
	UnlimitedMemory *int `json:"unlimitedMemory"`
}
