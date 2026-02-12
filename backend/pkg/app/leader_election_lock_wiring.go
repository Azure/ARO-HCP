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
	leaderElectionLockName      = "backend-leader"
	leaderElectionLeaseDuration = 15 * time.Second
	leaderElectionRenewDeadline = 10 * time.Second
	leaderElectionRetryPeriod   = 2 * time.Second
)

// NewLeaderElectionLock creates a new K8s leases resource lock, intended to be
// used for leader election in a Kubernetes cluster. leaseHolderIdentity the
// unique identifier of the participant across all participants in the election.
func NewLeaderElectionLock(leaseHolderIdentity string, kubeconfig *rest.Config, k8sNamespace string) (resourcelock.Interface, error) {
	leaderElectionLock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		k8sNamespace,
		leaderElectionLockName,
		resourcelock.ResourceLockConfig{
			Identity: leaseHolderIdentity,
		},
		kubeconfig,
		leaderElectionRenewDeadline,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create leader election lock: %w", err))
	}

	return leaderElectionLock, nil
}
