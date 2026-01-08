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

package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
)

// LeaderElectionConfig holds configuration for leader election
type LeaderElectionConfig struct {
	LockName      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
	Namespace     string
	KubeConfig    *rest.Config
}

func RunWithLeaderElection(ctx context.Context, config *LeaderElectionConfig, run func()) error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname for leader election: %w", err)
	}

	// Create leader election lock
	lock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		config.Namespace,
		config.LockName,
		resourcelock.ResourceLockConfig{
			Identity: hostname,
		},
		config.KubeConfig,
		config.RenewDeadline,
	)
	if err != nil {
		return fmt.Errorf("failed to create leader election lock: %w", err)
	}

	klog.V(2).Info("Leader election configured",
		"lockName", config.LockName,
		"identity", hostname,
		"leaseDuration", config.LeaseDuration,
		"renewDeadline", config.RenewDeadline,
		"retryPeriod", config.RetryPeriod)

	// Create leader elector
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   config.LeaseDuration,
		RenewDeadline:   config.RenewDeadline,
		RetryPeriod:     config.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            config.LockName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				klog.Info("Acquired leadership - starting control plane controller workers")
				run()
			},
			OnStoppedLeading: func() {
				klog.Info("Lost leadership - control plane controller workers stopped")
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	klog.Info("Starting leader election for control plane controller")
	le.Run(ctx)
	return nil
}
