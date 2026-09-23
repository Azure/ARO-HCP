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

package detectors

import (
	"testing"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller"
)

// TestSwiftNICResourceNameMatchesController pins the local copy of the extended
// resource name to the one the mgmt-agent advertises on the node. This package
// keeps its own copy so it does not depend on the controller package, so nothing
// but this test stops the two drifting apart.
func TestSwiftNICResourceNameMatchesController(t *testing.T) {
	if got, want := string(swiftNICResourceName), string(controller.SwiftNICResourceName); got != want {
		t.Errorf("swiftNICResourceName = %q, controller.SwiftNICResourceName = %q; they must stay equal", got, want)
	}
}
