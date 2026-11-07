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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
	clientID     string
	clientSecret string
	timeout      time.Duration
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
		Client(v.clientID, v.clientSecret).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %w", err)
	}
	defer connection.Close()

	diagnose := func(ctx context.Context, restConfig *rest.Config) string {
		var diag []string

		diagClient, err := configv1client.NewForConfig(restConfig)
		if err != nil {
			return fmt.Sprintf("failed to create config client for diagnostics: %v", err)
		}
		co, err := diagClient.ClusterOperators().Get(ctx, insightsOperatorName, metav1.GetOptions{})
		if err != nil {
			diag = append(diag, fmt.Sprintf("ClusterOperator %q: %v", insightsOperatorName, err))
		} else {
			if len(co.Status.Conditions) == 0 {
				diag = append(diag, "ClusterOperator conditions: EMPTY (no conditions set)")
			}
			for _, c := range co.Status.Conditions {
				diag = append(diag, fmt.Sprintf("  %s=%s (reason=%s, message=%s)", c.Type, c.Status, c.Reason, c.Message))
			}
			for _, v := range co.Status.Versions {
				diag = append(diag, fmt.Sprintf("  version: %s=%s", v.Name, v.Version))
			}
			for _, ro := range co.Status.RelatedObjects {
				diag = append(diag, fmt.Sprintf("  relatedObject: %s/%s %s/%s", ro.Group, ro.Resource, ro.Namespace, ro.Name))
			}
		}

		kubeClient, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			diag = append(diag, fmt.Sprintf("failed to create kube client: %v", err))
		} else {
			for _, ref := range []struct{ ns, name string }{
				{"openshift-config", "pull-secret"},
				{"kube-system", "global-pull-secret"},
			} {
				secret, sErr := kubeClient.CoreV1().Secrets(ref.ns).Get(ctx, ref.name, metav1.GetOptions{})
				if sErr != nil {
					diag = append(diag, fmt.Sprintf("%s/%s: %v", ref.ns, ref.name, sErr))
					continue
				}
				var cfg struct {
					Auths map[string]json.RawMessage `json:"auths"`
				}
				if jErr := json.Unmarshal(secret.Data[".dockerconfigjson"], &cfg); jErr != nil {
					diag = append(diag, fmt.Sprintf("%s/%s: failed to parse: %v", ref.ns, ref.name, jErr))
					continue
				}
				var hosts []string
				for h := range cfg.Auths {
					hosts = append(hosts, h)
				}
				diag = append(diag, fmt.Sprintf("%s/%s auths: %v", ref.ns, ref.name, hosts))
			}
		}

		return strings.Join(diag, "\n")
	}

	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, diagnose, func(ctx context.Context) error {
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
// it is visible in console.redhat.com.
func VerifyClusterVisibleInConsole(clientID, clientSecret string, timeout time.Duration) HostedClusterVerifier {
	return verifyClusterVisibleInConsole{clientID: clientID, clientSecret: clientSecret, timeout: timeout}
}
