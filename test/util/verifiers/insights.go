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

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
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

	disabled := getClusterOperatorCondition(co.Status.Conditions, "Disabled")
	if disabled != nil && disabled.Status == configv1.ConditionTrue {
		return fmt.Errorf("ClusterOperator %q is still Disabled (reason=%s, %s)", insightsOperatorName, disabled.Reason, formatConditions(co.Status.Conditions))
	}

	degraded := getClusterOperatorCondition(co.Status.Conditions, configv1.OperatorDegraded)
	if degraded != nil && degraded.Status == configv1.ConditionTrue {
		return fmt.Errorf("ClusterOperator %q is Degraded (%s)", insightsOperatorName, formatConditions(co.Status.Conditions))
	}

	return nil
}

// VerifyInsightsOperatorHealthy polls until the "insights" ClusterOperator
// reports Disabled!=True and Degraded!=True, or the timeout expires.
func VerifyInsightsOperatorHealthy(timeout time.Duration) HostedClusterVerifier {
	return verifyInsightsOperatorHealthy{timeout: timeout}
}

type verifyClusterVisibleInConsole struct {
	token   string
	timeout time.Duration
}

func (v verifyClusterVisibleInConsole) Name() string {
	return "VerifyClusterVisibleInConsole"
}

func (v verifyClusterVisibleInConsole) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ClusterVersion: %w", err)
	}

	clusterID := string(clusterVersion.Spec.ClusterID)
	if clusterID == "" {
		return fmt.Errorf("ClusterVersion spec.clusterID is empty")
	}

	connection, err := ocmsdk.NewConnectionBuilder().
		Tokens(v.token).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %w", err)
	}
	defer connection.Close()

	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		response, err := connection.ClustersMgmt().V1().Clusters().List().
			Search(fmt.Sprintf("external_id = '%s'", clusterID)).
			Size(1).
			SendContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to query OCM clusters API for external_id %q: %w", clusterID, err)
		}

		if response.Total() == 0 {
			return fmt.Errorf("cluster with external_id %q not yet visible in console.redhat.com", clusterID)
		}

		return nil
	})
}

// VerifyClusterVisibleInConsole polls the OCM clusters_mgmt API until the
// cluster (identified by ClusterVersion.Spec.ClusterID) appears, confirming
// the Insights Operator has registered it with console.redhat.com.
func VerifyClusterVisibleInConsole(token string, timeout time.Duration) HostedClusterVerifier {
	return verifyClusterVisibleInConsole{token: token, timeout: timeout}
}
