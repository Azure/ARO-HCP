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

package verifiers

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	"github.com/onsi/ginkgo/v2"

	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// VerifyClusterActiveVersionsSettledAtMinor checks that the cluster's ARM-reported
// activeVersions holds exactly one minor, equal to targetMinor, meaning the control plane has
// finished rolling onto it.
//
// activeVersions is derived from HostedCluster status.controlPlaneVersion by the backend
// ControlPlaneActiveVersions controller, which walks that history newest-first and stops at the
// first Completed entry. While an upgrade is in flight the list holds the in-progress version
// plus the last completed one; once the new version completes it holds only that one. It
// therefore reports control-plane completion independent of data-plane health.
//
// An absent or empty activeVersions list is reported as a retryable "not observed yet" error
// rather than treated as success: the field is written asynchronously, and the controller has
// no guard against transiently publishing an empty list when the observed HostedCluster is
// momentarily without status.
func VerifyClusterActiveVersionsSettledAtMinor(
	ctx context.Context,
	hcpClient *hcpsdk20261001.HcpOpenShiftClustersClient,
	resourceGroupName string,
	clusterName string,
	targetMinor string,
) error {
	parsedTarget, err := semver.ParseTolerant(targetMinor)
	if err != nil {
		return fmt.Errorf("parse target minor %q: %w", targetMinor, err)
	}
	target := minorOf(parsedTarget)

	resp, err := hcpClient.Get(ctx, resourceGroupName, clusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to get cluster %q via 2026-10-01: %w", clusterName, err)
	}

	if resp.Properties == nil || resp.Properties.Status == nil || len(resp.Properties.Status.ActiveVersions) == 0 {
		return fmt.Errorf("cluster %q reports no activeVersions yet; the backend has not published control plane versions", clusterName)
	}

	observed := make([]string, 0, len(resp.Properties.Status.ActiveVersions))
	for i, activeVersion := range resp.Properties.Status.ActiveVersions {
		if activeVersion == nil || activeVersion.Version == nil || len(*activeVersion.Version) == 0 {
			return fmt.Errorf("cluster %q activeVersions[%d] has no version", clusterName, i)
		}
		parsed, err := semver.ParseTolerant(*activeVersion.Version)
		if err != nil {
			return fmt.Errorf("parse cluster %q activeVersions[%d] %q: %w", clusterName, i, *activeVersion.Version, err)
		}
		observed = append(observed, minorOf(parsed))
	}

	ginkgo.GinkgoLogr.Info("cluster activeVersions observed",
		"cluster", clusterName, "activeVersions", observed, "targetMinor", target)

	if len(observed) != 1 || observed[0] != target {
		return fmt.Errorf("cluster %q activeVersions are %v, want exactly [%s]", clusterName, observed, target)
	}

	return nil
}

// minorOf renders a parsed version as the major.minor string ARM reports in activeVersions.
func minorOf(v semver.Version) string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}
