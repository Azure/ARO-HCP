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
	"net/http"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	internalversion "github.com/Azure/ARO-HCP/internal/version"
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

	ginkgo.GinkgoLogr.Info("Retrieved openshift cluster version history", "history", clusterVersion.Status.History)

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

	// Query Cincinnati to independently resolve what version should have been selected.
	// Cincinnati data may have changed since cluster creation, so a mismatch is logged
	// as a warning rather than causing a hard failure.
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	}
	cincinnatiClient := cvocincinnati.NewClient(uuid.New(), transport, "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())

	expectedVersion, err := internalversion.ResolveInitialVersion(ctx, cincinnatiClient, v.channelGroup, v.customerDesiredMinor)
	if err != nil {
		ginkgo.GinkgoLogr.Info("WARNING: failed to resolve expected version from Cincinnati (non-fatal)", "error", err)
		return nil
	}

	ginkgo.GinkgoLogr.Info("Compared installed version against Cincinnati-resolved version",
		"installed", installedVersion.String(), "resolved", expectedVersion.String())

	if !installedVersion.EQ(expectedVersion) {
		ginkgo.GinkgoLogr.Info("WARNING: installed version does not match current Cincinnati-resolved version; "+
			"this may be expected if Cincinnati data changed since cluster creation",
			"installed", installedVersion.String(), "resolved", expectedVersion.String())
	}

	return nil
}

// VerifyClusterInstalledVersion returns a verifier that checks the cluster was installed
// with a version in the correct minor stream (matching customerDesiredMinor).
// It also queries Cincinnati to compare against the expected resolved version.
func VerifyClusterInstalledVersion(customerDesiredMinor, channelGroup string) HostedClusterVerifier {
	return verifyClusterInstalledVersion{
		customerDesiredMinor: customerDesiredMinor,
		channelGroup:         channelGroup,
	}
}
