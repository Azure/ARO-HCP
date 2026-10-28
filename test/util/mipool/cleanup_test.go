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

package mipool

import (
	"context"
	"strings"
	"testing"
)

func TestScopeWithinResourceGroup(t *testing.T) {
	const container = "/subscriptions/sub/resourceGroups/aro-hcp-msi-container-dev"
	sibling := "/subscriptions/sub/resourceGroups/aro-hcp-msi-container-dev-a-00-00/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service"
	child := container + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service"

	if ScopeWithinResourceGroup(sibling, container) {
		t.Fatalf("sibling scope was selected: %s", sibling)
	}
	if !ScopeWithinResourceGroup(child, container) {
		t.Fatalf("child scope was not selected: %s", child)
	}
	if !ScopeWithinResourceGroup(strings.ToUpper(container), container) {
		t.Fatal("exact scope comparison should be case-insensitive")
	}
	if ScopeWithinResourceGroup(container, "") {
		t.Fatal("empty resource group scope should not match")
	}
}

func TestCleanupAllContainersNoContainers(t *testing.T) {
	err := CleanupAllContainers(context.Background(), nil, nil, "", nil, nil)
	if err != nil {
		t.Fatalf("expected nil error for empty containers, got: %v", err)
	}
}
