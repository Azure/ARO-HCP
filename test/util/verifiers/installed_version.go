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

	"github.com/Azure/ARO-HCP/internal/api"
)

type verifyClusterInstalledVersion struct {
	customerDesiredMinor string
	channelGroup         string
}

func (v verifyClusterInstalledVersion) Name() string {
	return fmt.Sprintf("VerifyClusterInstalledVersion(desiredMinor=%s, channelGroup=%s)", v.customerDesiredMinor, v.channelGroup)
}

func (v verifyClusterInstalledVersion) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
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

	// The oldest entry in the history is the initial install version.
	// History is ordered newest-first.
	initialEntry := clusterVersion.Status.History[len(clusterVersion.Status.History)-1]
	installedVersion, err := semver.ParseTolerant(initialEntry.Version)
	if err != nil {
		return fmt.Errorf("failed to parse installed version %q from history: %w", initialEntry.Version, err)
	}

	desiredMinor := api.Must(semver.ParseTolerant(v.customerDesiredMinor))

	if installedVersion.Major != desiredMinor.Major || installedVersion.Minor != desiredMinor.Minor {
		return fmt.Errorf("installed version %s has different major.minor than customer desired %s (expected %d.%d.x)",
			installedVersion.String(), v.customerDesiredMinor, desiredMinor.Major, desiredMinor.Minor)
	}

	ginkgo.GinkgoLogr.Info("Installed version is within expected minor", "installed", installedVersion.String(), "desiredMinor", v.customerDesiredMinor)

	return nil
}

// VerifyClusterInstalledVersion returns a verifier that checks the cluster was installed
// with a version in the correct minor stream (matching customerDesiredMinor).
func VerifyClusterInstalledVersion(customerDesiredMinor, channelGroup string) HostedClusterVerifier {
	return verifyClusterInstalledVersion{
		customerDesiredMinor: customerDesiredMinor,
		channelGroup:         channelGroup,
	}
}
