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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

type verifyControlPlaneInstalledVersion struct {
	customerDesiredMinor string
	preInstallResolved   semver.Version
	postInstallResolved  semver.Version
}

func (v verifyControlPlaneInstalledVersion) Name() string {
	return fmt.Sprintf("VerifyControlPlaneInstalledVersion(desiredMinor=%s, expectedRange=[%s, %s])",
		v.customerDesiredMinor, v.preInstallResolved, v.postInstallResolved)
}

func (v verifyControlPlaneInstalledVersion) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get clusterversion: %w", err)
	}

	if len(clusterVersion.Status.History) == 0 {
		return fmt.Errorf("clusterversion has no history entries")
	}

	ginkgo.GinkgoLogr.Info("Retrieved openshift cluster version history", "history", summarizeHistory(clusterVersion.Status.History))

	// History is ordered newest-first, so scan from the end to find the
	// oldest entry with a resolved version. An entry's .Version can be
	// empty if an upgrade was initiated before the version was resolved.
	var installedVersion semver.Version
	found := false
	for i := len(clusterVersion.Status.History) - 1; i >= 0; i-- {
		entry := clusterVersion.Status.History[i]
		if entry.Version == "" {
			continue
		}
		installedVersion, err = semver.ParseTolerant(entry.Version)
		if err != nil {
			return fmt.Errorf("failed to parse installed version %q from history: %w", entry.Version, err)
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("clusterversion history has no entries with a resolved version")
	}

	desiredMinor, err := semver.ParseTolerant(v.customerDesiredMinor)
	if err != nil {
		return fmt.Errorf("failed to parse customer desired minor %q: %w", v.customerDesiredMinor, err)
	}

	if installedVersion.Major != desiredMinor.Major || installedVersion.Minor != desiredMinor.Minor {
		return fmt.Errorf("installed version %s has different major.minor than customer desired %s (expected %d.%d.x)",
			installedVersion.String(), v.customerDesiredMinor, desiredMinor.Major, desiredMinor.Minor)
	}

	lower := v.preInstallResolved
	upper := v.postInstallResolved
	if upper.LT(lower) {
		lower, upper = upper, lower
	}

	if installedVersion.LT(lower) || installedVersion.GT(upper) {
		return fmt.Errorf("installed version %s is outside expected range [%s, %s]",
			installedVersion, lower, upper)
	}

	ginkgo.GinkgoLogr.Info("Installed version is within expected range",
		"installed", installedVersion.String(),
		"lower", lower.String(),
		"upper", upper.String())

	return nil
}

// VerifyControlPlaneInstalledVersion returns a verifier that checks the control plane
// was installed with a version in the correct minor stream (matching customerDesiredMinor)
// and within the range [min(preInstallResolved, postInstallResolved), max(...)].
// Resolve both bounds from Cincinnati before and after cluster creation to account for
// Cincinnati data changing during the long-running creation.
func VerifyControlPlaneInstalledVersion(customerDesiredMinor string, preInstallResolved, postInstallResolved semver.Version) HostedClusterVerifier {
	return verifyControlPlaneInstalledVersion{
		customerDesiredMinor: customerDesiredMinor,
		preInstallResolved:   preInstallResolved,
		postInstallResolved:  postInstallResolved,
	}
}
