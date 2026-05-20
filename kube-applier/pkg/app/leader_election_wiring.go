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

package app

import (
	"fmt"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	leaderElectionLeaseDuration = 15 * time.Second
	leaderElectionRenewDeadline = 10 * time.Second
	leaderElectionRetryPeriod   = 2 * time.Second
)

// NewLeaderElectionLock builds a Leases-backed lock in kubeNamespace named
// leaseName. leaseHolderIdentity should be the pod hostname so concurrent
// replicas distinguish themselves. The lock is constructed off a copy of
// kubeconfig with elevated QPS/Burst so renewals are never throttled by other
// API traffic sharing the same client.
func NewLeaderElectionLock(
	leaseHolderIdentity string,
	kubeconfig *rest.Config,
	kubeNamespace string,
	leaseName string,
) (resourcelock.Interface, error) {
	leKubeconfig := rest.CopyConfig(kubeconfig)
	leKubeconfig.QPS = 20
	leKubeconfig.Burst = 40

	lock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		kubeNamespace,
		leaseName,
		resourcelock.ResourceLockConfig{Identity: leaseHolderIdentity},
		leKubeconfig,
		leaderElectionRenewDeadline,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create leader election lock: %w", err))
	}
	return lock, nil
}
