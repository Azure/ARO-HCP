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
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	LeaderElectionLeaseDuration = 15 * time.Second
	LeaderElectionRenewDeadline = 10 * time.Second
	LeaderElectionRetryPeriod   = 2 * time.Second
)

// NewLeaderElectionLock builds a Leases-backed lock. The identity should be the
// pod hostname so concurrent replicas distinguish themselves.
func NewLeaderElectionLock(
	identity string,
	kubeconfig *rest.Config,
	namespace string,
	leaseName string,
) (resourcelock.Interface, error) {
	leaderElectionKubeconfig := rest.CopyConfig(kubeconfig)
	leaderElectionKubeconfig.QPS = 20
	leaderElectionKubeconfig.Burst = 40

	lock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		namespace,
		leaseName,
		resourcelock.ResourceLockConfig{Identity: identity},
		leaderElectionKubeconfig,
		LeaderElectionRenewDeadline,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create leader election lock: %w", err)
	}
	return lock, nil
}
