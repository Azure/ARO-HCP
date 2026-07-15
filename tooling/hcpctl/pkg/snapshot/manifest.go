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

package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest describes the complete set of diagnostic data gathered for a test or resource.
// The top-level manifest provides an overview; per-phase manifests hold resource and
// request details to avoid duplication.
type Manifest struct {
	// TestName is the name of the test that failed, if this snapshot was gathered
	// from a test failure. Empty when gathered directly from a resource ID.
	TestName string `json:"test_name,omitempty"`

	// ProwJobURL is the URL to the Prow job that triggered this snapshot, if applicable.
	ProwJobURL string `json:"prow_job_url,omitempty"`

	// TimeWindow is the full time range over which diagnostic data was gathered.
	TimeWindow TimeWindow `json:"time_window"`

	// ResourceGroup is the Azure resource group that was queried.
	ResourceGroup string `json:"resource_group"`

	// KustoCluster is the Kusto cluster endpoint used for queries.
	KustoCluster string `json:"kusto_cluster"`

	// KustoDatabase is the Kusto database used for queries.
	KustoDatabase string `json:"kusto_database"`

	// Phases lists the per-phase manifests for the test and cleanup phases.
	Phases []PhaseManifest `json:"phases"`

	// NodeConsoleLogs lists VM serial console log files captured from nodes
	// that were part of the test. These are written by the test framework when
	// node pool creation fails, providing boot diagnostic output.
	NodeConsoleLogs []NodeConsoleLog `json:"node_console_logs,omitempty"`

	// DirectoryLayout describes the output directory structure.
	DirectoryLayout map[string]string `json:"directory_layout"`
}

// PhaseManifest describes the diagnostic data gathered for a single phase (test_phase or cleanup_phase).
type PhaseManifest struct {
	// Name is the phase name ("test_phase" or "cleanup_phase").
	Name string `json:"name"`

	// Dir is the phase output directory, relative to the snapshot root.
	Dir string `json:"dir"`

	// Start is the phase start time.
	Start time.Time `json:"start"`

	// End is the phase end time.
	End time.Time `json:"end"`

	// Resources lists each ARM resource for which diagnostic data was gathered in this phase.
	Resources []ResourceEntry `json:"resources"`
}

// TimeWindow represents the full bounded time range for diagnostic queries, including
// phase boundary timestamps derived from test timing metadata.
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`

	// SetupFinishTime is when identity container setup completed. Zero when
	// no setup steps are present in the timing metadata.
	SetupFinishTime time.Time `json:"setup_finish_time,omitempty"`

	// TestStartTime is when the first non-setup step began. Zero when no
	// non-setup steps are present in the timing metadata.
	TestStartTime time.Time `json:"test_start_time,omitempty"`

	// CleanupStartTime is when test cleanup began.
	CleanupStartTime time.Time `json:"cleanup_start_time,omitempty"`
}

// ResourceEntry describes diagnostic data gathered for a single ARM resource.
type ResourceEntry struct {
	// Type is the ARM resource type (e.g. "Microsoft.RedHatOpenShift/hcpOpenShiftClusters").
	Type string `json:"type"`

	// Name is the ARM resource name.
	Name string `json:"name"`

	// Dir is the path to this resource's output directory, relative to the snapshot root.
	Dir string `json:"dir"`

	// ResourceID is the full ARM resource ID.
	ResourceID string `json:"resource_id,omitempty"`

	// ClusterResourceID is the full ARM resource ID of the parent HCP cluster.
	ClusterResourceID string `json:"cluster_resource_id,omitempty"`

	// ClusterResourceName is the HCP cluster name (the parent cluster for child resources).
	ClusterResourceName string `json:"cluster_resource_name,omitempty"`

	// InternalID is the internal resource identifier discovered from backend logs.
	InternalID string `json:"internal_id,omitempty"`

	// ClusterID is the Clusters Service identifier for this cluster.
	ClusterID string `json:"cluster_id,omitempty"`

	// HostedClusterNamespace is the management cluster namespace for the hosted cluster.
	HostedClusterNamespace string `json:"hosted_cluster_namespace,omitempty"`

	// HostedControlPlaneNamespace is the management cluster namespace for the hosted control plane.
	HostedControlPlaneNamespace string `json:"hosted_control_plane_namespace,omitempty"`

	// Requests lists the ARM requests traced for this resource.
	Requests []RequestInfo `json:"requests,omitempty"`
}

// RequestInfo describes a single ARM request traced during diagnostic gathering.
type RequestInfo struct {
	// ClientRequestID is the unique client request identifier.
	ClientRequestID string `json:"client_request_id"`

	// CorrelationID is the correlation identifier grouping related requests.
	CorrelationID string `json:"correlation_id"`

	// Method is the HTTP method (GET, PUT, DELETE, etc.).
	Method string `json:"method"`

	// Path is the ARM resource path.
	Path string `json:"path"`

	// Status is the HTTP response status code.
	Status int `json:"status"`

	// Timestamp is when the request was received.
	Timestamp time.Time `json:"timestamp"`

	// Dir is the path to this request's output directory, relative to the snapshot root.
	Dir string `json:"dir"`
}

// NodeConsoleLog describes a VM serial console log file captured from a node
// that was part of the test.
type NodeConsoleLog struct {
	// NodeName is the Azure VM name (e.g. "cilium-cluster-cilium-np-75x6r-4rwqj").
	NodeName string `json:"node_name"`

	// File is the path to the console log file, relative to the snapshot root
	// (e.g. "node_boot_logs/cilium-cluster-cilium-np-75x6r-4rwqj-console.log").
	File string `json:"file"`

	// ArtifactURL is the gcsweb URL for downloading the original artifact from
	// the Prow job's GCS bucket.
	ArtifactURL string `json:"artifact_url"`
}

// directoryLayout returns the static directory layout descriptions.
func directoryLayout() map[string]string {
	return map[string]string{
		"discovery":      "discovery/ — phase-independent query results: frontend requests, resource IDs, cluster associations, etc.",
		"test_logs":      "test_logs/ — the test's error.log and output.log from the Prow job",
		"test_phase":     "test_phase/ — diagnostic data from the test phase (between test start and cleanup start)",
		"cleanup_phase":  "cleanup_phase/ — diagnostic data from the cleanup phase (between cleanup start and overall end)",
		"serviceEvents":  "<phase>/events/ — Kubernetes events for service-level components (frontend, backend, clusters-service, maestro) during the phase",
		"resourceEvents": "<phase>/resources/<type>/<name>/events/ — Kubernetes events specific to a resource's control plane during the phase",
		"state":          "<phase>/resources/<type>/<name>/state/ — time-windowed raw resource state dumps (ARM state, CS state, Maestro logs, etc.)",
		"conditions":     "<phase>/resources/<type>/<name>/conditions/ — status condition transition summaries (HyperShift conditions, controller conditions)",
		"logs":           "<phase>/resources/<type>/<name>/logs/ — filtered or aggregated container and audit logs (operator logs, Maestro server/agent logs)",
		"requests":       "<phase>/resources/<type>/<name>/requests/<METHOD>-<client_request_id>/ — per-request trace data with state/ and logs/ subdirectories",
		"node_boot_logs": "node_boot_logs/ — VM serial console output (boot diagnostics) from nodes in the test. Files are named <node-name>-console.log. Each entry in manifest.json node_console_logs includes an artifact_url for downloading the original from the Prow job artifacts.",
	}
}

// WriteManifest serializes the manifest to a manifest.json file in the given directory.
func WriteManifest(dir string, manifest *Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write manifest to %s: %w", path, err)
	}
	return nil
}

// writePhaseManifest serializes a phase manifest to a manifest.json file.
func writePhaseManifest(dir string, phase *PhaseManifest) error {
	data, err := json.MarshalIndent(phase, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal phase manifest: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create phase directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write phase manifest to %s: %w", path, err)
	}
	return nil
}
