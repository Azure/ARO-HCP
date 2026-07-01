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

package manager

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/Azure/ARO-HCP/internal/leaderelection"
)

// NewLeaderElectionLock builds a Leases-backed lock. The identity should be the
// pod hostname so concurrent replicas distinguish themselves.
func NewLeaderElectionLock(
	identity string,
	kubeconfig *rest.Config,
	namespace string,
	leaseName string,
) (resourcelock.Interface, error) {
	lock, err := leaderelection.NewLeaderElectionLock(identity, kubeconfig, namespace, leaseName, leaderelection.RecommendedRenewDeadline)
	if err != nil {
		return nil, fmt.Errorf("leader election lock %s/%s: %w", namespace, leaseName, err)
	}
	return lock, nil
}
