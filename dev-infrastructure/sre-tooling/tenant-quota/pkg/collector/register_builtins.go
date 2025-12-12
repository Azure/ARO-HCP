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

package collector

import (
	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/collectors/tenant"
)

func init() {
	// Register all built-in Go collectors here.

	// Register tenant-quota collector
	if err := Register("tenant-quota", CollectorFunc(tenant.CollectQuotaFunc())); err != nil {
		// This should never happen in normal operation, but panic if it does
		panic("failed to register tenant-quota collector: " + err.Error())
	}

	// Add new built-in Go collectors here as they are created:
	// Example:
	// if err := Register("cost-monitor", cost.CollectMonitorFunc()); err != nil {
	//     panic("failed to register cost-monitor collector: " + err.Error())
	// }
}
