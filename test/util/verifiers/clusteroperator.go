// Copyright 2025 Microsoft Corporation
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
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

type verifyAllClusterOperatorsAvailableImpl struct{}

func (v verifyAllClusterOperatorsAvailableImpl) Name() string {
	return "VerifyAllClusterOperatorsAvailable"
}

func (v verifyAllClusterOperatorsAvailableImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	start := time.Now()
	var lastErr error
	var lastErrMsg string
	verifyErr := wait.PollUntilContextTimeout(ctx, 20*time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		clusterOperators, err := configClient.ClusterOperators().List(ctx, metav1.ListOptions{})
		if err != nil {
			lastErr = fmt.Errorf("failed to list ClusterOperators: %w", err)
			if msg := lastErr.Error(); msg != lastErrMsg {
				ginkgo.GinkgoLogr.Info("Failed to list ClusterOperators, retrying.", "error", err)
				lastErrMsg = msg
			}
			return false, nil
		}

		if len(clusterOperators.Items) == 0 {
			lastErr = errors.New("no ClusterOperators found in the cluster")
			if msg := lastErr.Error(); msg != lastErrMsg {
				ginkgo.GinkgoLogr.Info("ClusterOperators not yet ready.", "error", lastErr)
				lastErrMsg = msg
			}
			return false, nil
		}

		unavailableOperators := []string{}
		for _, co := range clusterOperators.Items {
			availableCondition := getClusterOperatorCondition(co.Status.Conditions, configv1.OperatorAvailable)
			if availableCondition == nil {
				unavailableOperators = append(unavailableOperators, fmt.Sprintf("%s (no Available condition)", co.Name))
				continue
			}
			if availableCondition.Status != configv1.ConditionTrue {
				unavailableOperators = append(unavailableOperators, fmt.Sprintf("%s (%s)", co.Name, formatConditions(co.Status.Conditions)))
			}
		}

		if len(unavailableOperators) > 0 {
			slices.Sort(unavailableOperators)
			lastErr = fmt.Errorf("cluster operators not available: %s", strings.Join(unavailableOperators, ", "))
			if msg := lastErr.Error(); msg != lastErrMsg {
				ginkgo.GinkgoLogr.Info("ClusterOperators not yet ready.", "unavailable", unavailableOperators)
				lastErrMsg = msg
			}
			return false, nil
		}

		return true, nil
	})

	if verifyErr == nil {
		ginkgo.GinkgoLogr.Info("All cluster operators available", "duration", time.Since(start))
		return nil
	}

	return errors.Join(verifyErr, lastErr)
}

// readyRESTConfig returns a copy of adminRESTConfig with a per-request timeout and rate limiting disabled.
//
//	The gates make one request per poll interval, and a token-bucket limiter would also
//
// refuse a token once the poll deadline's timestamp has passed,
func readyRESTConfig(adminRESTConfig *rest.Config) *rest.Config {
	config := rest.CopyConfig(adminRESTConfig)
	config.Timeout = 10 * time.Second
	config.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	return config
}

// waitForClusterOperatorCondition polls until the named ClusterOperator reports the given condition
// with status True.
// The returned timeout error names the condition and the last observed reason/message.
func waitForClusterOperatorCondition(
	ctx context.Context,
	adminRESTConfig *rest.Config,
	operatorName string,
	conditionType configv1.ClusterStatusConditionType,
	timeout time.Duration,
	interval time.Duration,
) error {
	if adminRESTConfig == nil {
		return fmt.Errorf("%s %s readiness wait: admin REST config is nil", operatorName, conditionType)
	}
	if interval <= 0 {
		return fmt.Errorf("%s %s readiness wait: interval must be > 0, got %s", operatorName, conditionType, interval)
	}
	configClient, err := configv1client.NewForConfig(readyRESTConfig(adminRESTConfig))
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	name := fmt.Sprintf("VerifyClusterOperatorCondition(operator=%s, condition=%s)", operatorName, conditionType)
	return pollUntilReady(ctx, name, timeout, interval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		operator, err := configClient.ClusterOperators().Get(ctx, operatorName, metav1.GetOptions{})
		if err != nil {
			// A request failing while the poll's context has ended is an
			// interruption whose error may not wrap the context error; report the
			// context error so pollUntilReady keeps the last real observation.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to get ClusterOperator %q: %w", operatorName, err)
		}
		condition := getClusterOperatorCondition(operator.Status.Conditions, conditionType)
		switch {
		case condition == nil:
			return fmt.Errorf("ClusterOperator %q has no %s condition", operatorName, conditionType)
		case condition.Status != configv1.ConditionTrue:
			return fmt.Errorf("ClusterOperator %q %s=%s reason=%s message=%q",
				operatorName, conditionType, condition.Status, condition.Reason, condition.Message)
		}
		return nil
	})
}

func getClusterOperatorCondition(conditions []configv1.ClusterOperatorStatusCondition, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func formatConditions(conditions []configv1.ClusterOperatorStatusCondition) string {
	var parts []string
	for _, cond := range conditions {
		parts = append(parts, fmt.Sprintf("%s=%s", cond.Type, cond.Status))
	}
	return strings.Join(parts, ", ")
}

func VerifyAllClusterOperatorsAvailable() HostedClusterVerifier {
	return verifyAllClusterOperatorsAvailableImpl{}
}
