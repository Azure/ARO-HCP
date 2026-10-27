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

	degraded := getClusterOperatorCondition(co.Status.Conditions, configv1.OperatorDegraded)
	if degraded != nil && degraded.Status == configv1.ConditionTrue {
		return fmt.Errorf("ClusterOperator %q is Degraded (%s)", insightsOperatorName, formatConditions(co.Status.Conditions))
	}

	return nil
}

// VerifyInsightsOperatorHealthy polls until the "insights" ClusterOperator
// reports Degraded!=True, or the timeout expires.
func VerifyInsightsOperatorHealthy(timeout time.Duration) HostedClusterVerifier {
	return verifyInsightsOperatorHealthy{timeout: timeout}
}

type verifyInsightsOperatorDisabledNoToken struct {
	timeout time.Duration
}

func (v verifyInsightsOperatorDisabledNoToken) Name() string {
	return "VerifyInsightsOperatorDisabledNoToken"
}

func (v verifyInsightsOperatorDisabledNoToken) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		return v.checkOnce(ctx, adminRESTConfig)
	})
}

func (v verifyInsightsOperatorDisabledNoToken) checkOnce(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	co, err := configClient.ClusterOperators().Get(ctx, insightsOperatorName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ClusterOperator %q: %w", insightsOperatorName, err)
	}

	disabled := getClusterOperatorCondition(co.Status.Conditions, "Disabled")
	if disabled == nil {
		return fmt.Errorf("ClusterOperator %q has no Disabled condition yet (%s)", insightsOperatorName, formatConditions(co.Status.Conditions))
	}
	if disabled.Status != configv1.ConditionTrue {
		return fmt.Errorf("ClusterOperator %q Disabled=%s, expected True (%s)", insightsOperatorName, disabled.Status, formatConditions(co.Status.Conditions))
	}
	if disabled.Reason != "NoToken" {
		return fmt.Errorf("ClusterOperator %q Disabled reason=%q, expected NoToken (%s)", insightsOperatorName, disabled.Reason, formatConditions(co.Status.Conditions))
	}
	return nil
}

// VerifyInsightsOperatorDisabledNoToken polls until the "insights" ClusterOperator
// reports Disabled=True with Reason=NoToken, confirming the operator is deployed
// and correctly detects the absence of the cloud.openshift.com token.
func VerifyInsightsOperatorDisabledNoToken(timeout time.Duration) HostedClusterVerifier {
	return verifyInsightsOperatorDisabledNoToken{timeout: timeout}
}
