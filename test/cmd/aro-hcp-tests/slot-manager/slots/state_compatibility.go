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

package slots

import "strings"

// WriteEnvFile is the v1 acquisition compatibility writer, including E2E identity
// exports. It retains support for resolved v2 state, but v2 acquisition uses
// handler PublishLease contributions and the shared runtime contract builder.
func WriteEnvFile(sharedDir string, state *AcquiredSlotState, customerSubscription, selectedClusterProfileDir string) error {
	contract := NewRuntimeContractBuilder()
	if err := AddCoreRuntimeExports(contract, state, customerSubscription, selectedClusterProfileDir); err != nil {
		return err
	}
	if err := contract.Add("e2e-identities", "LEASED_MSI_CONTAINERS", strings.Join(state.Slot.IdentityContainerNames(), " ")); err != nil {
		return err
	}
	return WriteRuntimeContract(sharedDir, contract)
}
