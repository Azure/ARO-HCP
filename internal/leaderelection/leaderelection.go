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

// Package leaderelection provides shared leader-election constants and helpers
// for all ARO-HCP components that run controllers under a Kubernetes lease.
package leaderelection

import (
	"time"

	"github.com/go-logr/logr"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Recommended leader-election timing constants.
//
// These values are tuned so that:
//   - A kube-apiserver disruption of up to ~78 s is tolerated without the
//     leader changing (preventing unnecessary controller restarts during
//     rolling API-server upgrades).
//   - Graceful lease re-acquisition (e.g. during a new Deployment rollout
//     where the old leader releases) completes in at most one RetryPeriod
//     (26 s).
//   - Clock-skew tolerance between replicas is LeaseDuration − RenewDeadline
//     = 30 s.
//
// Derived properties (for the default values):
//
//	retryTimes                      = floor(RenewDeadline / RetryPeriod) = 4
//	clock skew tolerance            = LeaseDuration − RenewDeadline      = 30 s
//	kube-apiserver downtime tolerance = (retryTimes − 1) × RetryPeriod   = 78 s
//	worst non-graceful acquisition  = LeaseDuration + RetryPeriod        = 163 s
//	worst graceful acquisition      = RetryPeriod                        = 26 s
const (
	RecommendedLeaseDuration = 137 * time.Second
	RecommendedRenewDeadline = 107 * time.Second
	RecommendedRetryPeriod   = 26 * time.Second
)

// NewLeaderElectionLock builds a Leases-backed lock in the given namespace.
// identity should be the pod hostname so concurrent replicas distinguish
// themselves. renewDeadline is forwarded as the client-side timeout for the
// underlying REST calls; callers that override the default election timings
// (e.g. via CLI flags) should pass their actual RenewDeadline so the lock
// client timeout matches the configured election settings.
// The lock is constructed off a copy of kubeconfig with elevated QPS/Burst
// so renewals are never throttled by other API traffic sharing the same
// client.
func NewLeaderElectionLock(
	identity string,
	kubeconfig *rest.Config,
	namespace string,
	leaseName string,
	renewDeadline time.Duration,
) (resourcelock.Interface, error) {
	leKubeconfig := rest.CopyConfig(kubeconfig)
	leKubeconfig.QPS = 20
	leKubeconfig.Burst = 40

	lock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		namespace,
		leaseName,
		resourcelock.ResourceLockConfig{Identity: identity},
		leKubeconfig,
		renewDeadline,
	)
	if err != nil {
		return nil, err
	}
	return lock, nil
}

// LogLeaseProperties logs the key timing properties derived from a
// LeaderElectionConfig so operators can verify the leader-election
// tuning at startup without reading the source.
func LogLeaseProperties(logger logr.Logger, config leaderelection.LeaderElectionConfig) {
	if config.RetryPeriod <= 0 {
		logger.Info("leader election lease properties: skipping (RetryPeriod is zero or negative)")
		return
	}
	retryTimes := int(config.RenewDeadline / config.RetryPeriod)
	if retryTimes <= 0 {
		logger.Info("WARNING: leader election misconfiguration", "reason", "RenewDeadline < RetryPeriod, cannot compute meaningful downtime tolerance")
		return
	}
	downtimeTolerance := time.Duration(retryTimes-1) * config.RetryPeriod

	logger.Info("leader election lease properties",
		"retryTimes", retryTimes,
		"clockSkewTolerance", config.LeaseDuration-config.RenewDeadline,
		"kubeApiserverDowntimeTolerance", downtimeTolerance,
		"worstNonGracefulLeaseAcquisition", config.LeaseDuration+config.RetryPeriod,
		"worstGracefulLeaseAcquisition", config.RetryPeriod,
	)
}
