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

package nodehealth

const (
	// ControllerName is the single source of truth for this controller's name.
	// It feeds the workqueue name, which surfaces as a Prometheus label on the
	// workqueue metrics, so that label never drifts from the controller.
	ControllerName = "node-health"

	// fieldManager identifies this controller in server-side patch operations.
	fieldManager = "mgmt-agent-node-health"

	// labelKey is the node label whose presence marks a node as wedged. A
	// separate mitigation controller selects on this label.
	labelKey = "node-health.aro-hcp.azure.com/status"
	// labelValue is the value applied to labelKey on a wedged node.
	labelValue = "wedged"

	// annotationDetector records the name of the detector that fired.
	annotationDetector = "node-health.aro-hcp.azure.com/detector"
	// annotationReason records a short human-readable explanation for the label.
	annotationReason = "node-health.aro-hcp.azure.com/reason"
	// annotationObservedAt records the RFC3339 timestamp the node was first
	// labeled in the current wedge episode.
	annotationObservedAt = "node-health.aro-hcp.azure.com/observed-at"
)
