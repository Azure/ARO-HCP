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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationv1client "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/utils/set"
)

// APIServicesAvailableTimeout bounds how long the verifier waits for every aggregated
// APIService to report Available.
//
// HyperShift reports a HostedCluster as provisioned before the guest cluster's aggregated
// API servers are serving. In particular v1beta1.metrics.k8s.io is backed by metrics-server
// in openshift-monitoring, which only rolls out once the first worker nodes join, and the
// kube-aggregator reports an x509 verification failure against that endpoint until
// service-ca has issued its serving cert and the APIService caBundle has converged. That
// window is routinely a couple of minutes on a freshly created cluster, so the verifier has
// to tolerate it rather than fail a healthy-but-slow cluster.
const APIServicesAvailableTimeout = 5 * time.Minute

// apiServiceLister is the slice of the APIService client that checkOnce actually uses.
// Depending on this rather than the full client keeps the per-attempt logic unit-testable
// without the generated fake clientset, whose package is not vendored.
type apiServiceLister interface {
	List(ctx context.Context, opts metav1.ListOptions) (*apiregistrationv1.APIServiceList, error)
}

type verifyAllAPIServicesAvailableImpl struct {
	timeout time.Duration
}

func (v verifyAllAPIServicesAvailableImpl) Name() string {
	return "VerifyAllAPIServicesAvailable"
}

func (v verifyAllAPIServicesAvailableImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	apiserviceClient, err := apiregistrationv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		return v.checkOnce(ctx, apiserviceClient.APIServices())
	})
}

func (v verifyAllAPIServicesAvailableImpl) checkOnce(ctx context.Context, apiServices apiServiceLister) error {
	allAPIServices, err := apiServices.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list all APIServices: %w", err)
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
		return errors.Join(failureReasonErrs...)
	}
	if !foundAPIServices.HasAll(requiredAPIServices.UnsortedList()...) {
		return fmt.Errorf("required apiservices are missing: %v", strings.Join(requiredAPIServices.Difference(foundAPIServices).SortedList(), ", "))
	}
	return nil
}

func verifyAllAPIServicesAvailable() HostedClusterVerifier {
	return verifyAllAPIServicesAvailableImpl{timeout: APIServicesAvailableTimeout}
}

func getAPIServiceCondition(conditions []apiregistrationv1.APIServiceCondition, name string) *apiregistrationv1.APIServiceCondition {
	for _, condition := range conditions {
		if string(condition.Type) == name {
			return &condition
		}
	}
	return nil
}
