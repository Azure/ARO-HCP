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
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

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

	var lastErr error
	verifyErr := wait.PollUntilContextTimeout(ctx, 20*time.Second, 30*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		clusterOperators, err := configClient.ClusterOperators().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list ClusterOperators: %w", err)
		}

		if len(clusterOperators.Items) == 0 {
			lastErr = errors.New("no ClusterOperators found in the cluster")
			ginkgo.GinkgoLogr.Info("ClusterOperators not yet ready.", "error", lastErr)
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
			lastErr = fmt.Errorf("cluster operators not available: %s", strings.Join(unavailableOperators, ", "))
			ginkgo.GinkgoLogr.Info("ClusterOperators not yet ready.", "unavailable", unavailableOperators)
			return false, nil
		}

		return true, nil
	})

	if verifyErr == nil {
		return nil
	}
	if lastErr != nil {
		return lastErr
	}

	return verifyErr
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
