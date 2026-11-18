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
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

// clusterVersionFailing is the ClusterVersion condition the cluster-version-operator uses to report
// why it cannot finish applying a release. openshift/api does not define a constant for it.
const clusterVersionFailing configv1.ClusterStatusConditionType = "Failing"

type verifyClusterVersionRolloutCompleted struct {
	timeout time.Duration
}

func (v verifyClusterVersionRolloutCompleted) Name() string {
	return "VerifyClusterVersionRolloutCompleted"
}

func (v verifyClusterVersionRolloutCompleted) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get clusterversion %q: %w", "version", err)
		}
		return clusterVersionRolloutCompleted(clusterVersion)
	})
}

// clusterVersionRolloutCompleted returns nil when every status.history entry is Completed. Otherwise
// it returns an error naming the incomplete entries and the Failing and Progressing messages, which
// list the cluster operators the rollout is waiting on.
func clusterVersionRolloutCompleted(clusterVersion *configv1.ClusterVersion) error {
	if len(clusterVersion.Status.History) == 0 {
		return errors.New("clusterversion/version has no status.history entries yet")
	}

	var incomplete []string
	for _, entry := range clusterVersion.Status.History {
		if entry.State != configv1.CompletedUpdate {
			incomplete = append(incomplete, fmt.Sprintf("%s=%q", entry.Version, entry.State))
		}
	}
	if len(incomplete) == 0 {
		return nil
	}

	details := []string{"history: " + strings.Join(incomplete, ", ")}
	for _, conditionType := range []configv1.ClusterStatusConditionType{clusterVersionFailing, configv1.OperatorProgressing} {
		if condition := getClusterOperatorCondition(clusterVersion.Status.Conditions, conditionType); condition != nil && condition.Message != "" {
			details = append(details, fmt.Sprintf("%s=%s: %s", condition.Type, condition.Status, condition.Message))
		}
	}
	return fmt.Errorf("clusterversion/version rollout not completed, expected every status.history entry to be %s: %s",
		configv1.CompletedUpdate, strings.Join(details, "; "))
}

// VerifyClusterVersionRolloutCompleted polls until every entry in ClusterVersion status.history is
// Completed, which the cluster-version-operator only reports once every cluster operator has rolled
// out the release. This matches the version rollout wait in the HyperShift e2e v2 create-guests
// phase, which blocks on the same history mirrored into HostedCluster status.version.
func VerifyClusterVersionRolloutCompleted(timeout time.Duration) HostedClusterVerifier {
	return verifyClusterVersionRolloutCompleted{timeout: timeout}
}
