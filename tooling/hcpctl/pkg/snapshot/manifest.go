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
type Manifest struct {
	// TestName is the name of the test that failed, if this snapshot was gathered
	// from a test failure. Empty when gathered directly from a resource ID.
	TestName string `json:"test_name,omitempty"`

	// ProwJobURL is the URL to the Prow job that triggered this snapshot, if applicable.
	ProwJobURL string `json:"prow_job_url,omitempty"`

	// TimeWindow is the time range over which diagnostic data was gathered.
	TimeWindow TimeWindow `json:"time_window"`

	// ResourceGroup is the Azure resource group that was queried.
	ResourceGroup string `json:"resource_group"`

	// KustoCluster is the Kusto cluster endpoint used for queries.
	KustoCluster string `json:"kusto_cluster"`

	// KustoDatabase is the Kusto database used for queries.
	KustoDatabase string `json:"kusto_database"`

	// Resources lists each ARM resource for which diagnostic data was gathered.
	Resources []ResourceEntry `json:"resources"`

	// DirectoryLayout describes the output directory structure.
	DirectoryLayout map[string]string `json:"directory_layout"`
}

// TimeWindow represents a bounded time range for diagnostic queries.
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
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

	// BundleIDs lists the Maestro bundle IDs associated with this cluster.
	BundleIDs []string `json:"bundle_ids,omitempty"`

	// ManifestWorkNames lists the ManifestWork namespace/name pairs for this cluster.
	ManifestWorkNames []string `json:"manifest_work_names,omitempty"`
}

// directoryLayout returns the static directory layout descriptions.
func directoryLayout() map[string]string {
	return map[string]string{
		"context":   "context/ — resource-group-scoped event logs and the full list of frontend requests",
		"discovery": "resources/<type>/<name>/discovery/ — intermediate query results used to derive IDs, cluster associations, etc.",
		"state":     "resources/<type>/<name>/state/ — time-windowed state snapshots for each resource (ARM state, CS state, HyperShift conditions, Maestro logs, etc.)",
		"requests":  "resources/<type>/<name>/requests/<correlation_id>/ — per-request trace data (async operation state, polling history)",
		"summary":   "resources/<type>/<name>/SUMMARY.md — per-resource summary of discovered facts, requests, and skipped queries",
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
