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
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationv1client "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/utils/set"
)

type verifyAllAPIServicesAvailableImpl struct{}

func (v verifyAllAPIServicesAvailableImpl) Name() string {
	return "VerifyAllAPIServicesAvailable"
}

func (v verifyAllAPIServicesAvailableImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	apiserviceClient, err := apiregistrationv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	retryCtx, cancel := context.WithTimeoutCause(ctx, 5*time.Minute, errors.New("API service retry timeout"))
	defer cancel()

	attempts := 0
	var lastErr error
	verifyErr := wait.ExponentialBackoffWithContext(retryCtx, wait.Backoff{
		Duration: 800 * time.Millisecond,
		Factor:   2,
		Jitter:   0.1,
		Steps:    10,
		Cap:      20 * time.Second,
	}, func(ctx context.Context) (done bool, err error) {
		attempts++

		allAPIServices, err := apiserviceClient.APIServices().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to list all APIServices: %w", err)
		}

		requiredAPIServices := set.New("v1.route.openshift.io")
		foundAPIServices := set.Set[string]{}
		unavailableAPIServices := []apiregistrationv1.APIService{}
		for _, apiService := range allAPIServices.Items {
			foundAPIServices.Insert(apiService.Name)
			availableConditon := getAPIServiceCondition(apiService.Status.Conditions, "Available")
			if availableConditon == nil {
				unavailableAPIServices = append(unavailableAPIServices, apiService)
				continue
			}
			if availableConditon.Status != "True" {
				unavailableAPIServices = append(unavailableAPIServices, apiService)
				continue
			}
		}
		if len(unavailableAPIServices) != 0 {
			failureReasonErrs := []error{}
			for _, unavailableAPIService := range unavailableAPIServices {
				availableCondition := getAPIServiceCondition(unavailableAPIService.Status.Conditions, "Available")
				if availableCondition == nil {
					failureReasonErrs = append(failureReasonErrs, fmt.Errorf("apiservice/%s is not available because it has no status", unavailableAPIService.Name))
				} else {
					failureReasonErrs = append(failureReasonErrs, fmt.Errorf("apiservice/%s is not available because: %v", unavailableAPIService.Name, availableCondition.Message))
				}
			}
			ginkgo.GinkgoLogr.Info("APIServices not yet ready.", "errors", errors.Join(failureReasonErrs...))
			lastErr = errors.Join(failureReasonErrs...)
			return false, nil
		}
		if !foundAPIServices.HasAll(requiredAPIServices.UnsortedList()...) {
			ginkgo.GinkgoLogr.Info("required apiservices are missing", "missing", strings.Join(requiredAPIServices.Difference(foundAPIServices).SortedList(), ", "))
			lastErr = fmt.Errorf("required apiservices are missing: %v", strings.Join(requiredAPIServices.Difference(foundAPIServices).SortedList(), ", "))
			return false, nil
		}
		return true, nil
	})

	deadline, err := time.Parse(time.RFC3339, "2026-01-15T00:00:00Z")
	if err != nil {
		return fmt.Errorf("failed to parse deadline: %w", err)
	}

	if attempts > 1 && time.Now().After(deadline) {
		return errors.New("the APIService verifier is not allowed to re-try after Jan 15th, 2026 - HyperShift should only post ready status once aggregated APIs are ready then")
	}

	if verifyErr == nil {
		// even if we had an error at some point, the loop succeeded on the last run
		return nil
	}
	if lastErr != nil {
		// if the loop failed, and we have some specific error that caused it, return that so
		// our consumers don't just get a "context deadline exceeded"
		return lastErr
	}

	return verifyErr
}

func verifyAllAPIServicesAvailable() HostedClusterVerifier {
	return verifyAllAPIServicesAvailableImpl{}
}

func getAPIServiceCondition(conditions []apiregistrationv1.APIServiceCondition, name string) *apiregistrationv1.APIServiceCondition {
	for _, condition := range conditions {
		if string(condition.Type) == name {
			return &condition
		}
	}
	return nil
}
