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

package controlplane

// Condition types for Session resources
const (
	// ConditionTypeReady indicates the overall operational state of the session
	ConditionTypeReady = "Ready"
	// ConditionTypeProgressing indicates active reconciliation
	ConditionTypeProgressing = "Progressing"
	// ConditionTypeDegraded indicates permanent configuration errors
	ConditionTypeDegraded = "Degraded"
	// ConditionTypeAvailable indicates endpoint accessibility
	ConditionTypeAvailable = "Available"
	// ConditionTypeCredentials indicates the status of credential provisioning
	ConditionTypeCredentials = "Credentials"
)
