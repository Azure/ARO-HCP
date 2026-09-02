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
