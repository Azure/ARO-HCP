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

package namespacedeletion

import (
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
)

// Default safety margins. Both are overridable so that end-to-end tests, which
// all gate on them, do not have to wait out a production-sized window.
const (
	// defaultMinimumDwell is a chosen initial policy, not evidence of safety.
	// It is set to be comfortably longer than a healthy HostedCluster teardown
	// (minutes), not tuned to the observed orphan population, which is far
	// older.
	defaultMinimumDwell = time.Hour

	// defaultHostedClusterRemovalTimeout bounds the opt-in path's wait for
	// HyperShift to finish. Long enough for a healthy deprovision, short enough
	// that a wedged one is reported the same working day.
	defaultHostedClusterRemovalTimeout = 30 * time.Minute
)

func registerNamespaceDeletionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers: 1,
		// usesUnion must be true: this controller reads both union desire
		// listers, and WithCacheSyncs only adds the union readiness barrier
		// when it is. Without it a restart can read a cold cache, and a false
		// "absent" reading is exactly what authorizes an irreversible mutation
		// here.
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNamespaceDeletionController, true),
	}
}

func instantiateNamespaceDeletionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, readDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	_, applyDesireLister := controllerContext.UnionKubeApplierInformers.ApplyDesires()
	return NewNamespaceDeletionController(
		controllerContext.FleetDBClient,
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		readDesireLister,
		applyDesireLister,
		controllerContext.FleetInformers,
		Config{
			MinimumDwell:                defaultMinimumDwell,
			HostedClusterRemovalTimeout: defaultHostedClusterRemovalTimeout,
		},
	), nil
}

// Register adds this package's controllers to the backend registry.
func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(NamespaceDeletionControllerName)] = registerNamespaceDeletionController()
}
