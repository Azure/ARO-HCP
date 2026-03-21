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

package controller

import (
	"fmt"
)

const (
	// ControllerAgentName distinguishes this controller from other things writing to API objects
	ControllerAgentName = "hcp-recovery-controller"

	// LabelManagedBy identifies resources managed by the hcp-recovery controller
	LabelManagedBy = "app.kubernetes.io/managed-by"

	// TODO: Add additional constants as needed, e.g.:
	// - Annotation keys for cross-cluster ownership
	// - Timeout durations
	// - Condition type strings
)

// ManagedByLabelSelector returns a label selector string for resources managed by this controller.
// This is used to filter informers to only watch resources created and managed by hcp-recovery-controller.
func ManagedByLabelSelector() string {
	return fmt.Sprintf("%s=%s", LabelManagedBy, ControllerAgentName)
}
