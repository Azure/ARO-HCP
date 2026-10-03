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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

const insightsOperatorName = "insights"

type verifyInsightsOperatorHealthy struct {
	timeout time.Duration
}

func (v verifyInsightsOperatorHealthy) Name() string {
	return "VerifyInsightsOperatorHealthy"
}

func (v verifyInsightsOperatorHealthy) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		return v.checkOnce(ctx, adminRESTConfig)
	})
}

func (v verifyInsightsOperatorHealthy) checkOnce(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	co, err := configClient.ClusterOperators().Get(ctx, insightsOperatorName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ClusterOperator %q: %w", insightsOperatorName, err)
	}

	available := getClusterOperatorCondition(co.Status.Conditions, configv1.OperatorAvailable)
	if available == nil || available.Status != configv1.ConditionTrue {
		return fmt.Errorf("ClusterOperator %q is not Available (%s)", insightsOperatorName, formatConditions(co.Status.Conditions))
	}

	degraded := getClusterOperatorCondition(co.Status.Conditions, configv1.OperatorDegraded)
	if degraded != nil && degraded.Status == configv1.ConditionTrue {
		return fmt.Errorf("ClusterOperator %q is Degraded (%s)", insightsOperatorName, formatConditions(co.Status.Conditions))
	}

	return nil
}

// VerifyInsightsOperatorHealthy polls until the "insights" ClusterOperator
// reports Available=True and Degraded=False, or the timeout expires.
// This verifies that the Insights Operator has detected a valid
// cloud.openshift.com token (e.g. from kube-system/global-pull-secret)
// and successfully registered the cluster.
func VerifyInsightsOperatorHealthy(timeout time.Duration) HostedClusterVerifier {
	return verifyInsightsOperatorHealthy{timeout: timeout}
}
