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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

// fakeAPIServiceLister returns a fixed set of APIServices, or an error if one is set.
type fakeAPIServiceLister struct {
	items []apiregistrationv1.APIService
	err   error
}

func (f fakeAPIServiceLister) List(context.Context, metav1.ListOptions) (*apiregistrationv1.APIServiceList, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &apiregistrationv1.APIServiceList{Items: f.items}, nil
}

func apiService(name string, conditions ...apiregistrationv1.APIServiceCondition) apiregistrationv1.APIService {
	return apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     apiregistrationv1.APIServiceStatus{Conditions: conditions},
	}
}

func availableCondition(status apiregistrationv1.ConditionStatus, message string) apiregistrationv1.APIServiceCondition {
	return apiregistrationv1.APIServiceCondition{
		Type:    apiregistrationv1.Available,
		Status:  status,
		Message: message,
	}
}

// TestVerifyAllAPIServicesAvailable_ConstructorSetsTimeout guards the zero-value trap:
// pollUntilReady rejects a non-positive timeout before it ever polls, so a verifier built
// as verifyAllAPIServicesAvailableImpl{} - the shape still used by sibling constructors
// such as verifyBasicAccess() - would fail every HCP verification instantly rather than
// waiting for the aggregated APIs.
func TestVerifyAllAPIServicesAvailable_ConstructorSetsTimeout(t *testing.T) {
	verifier, ok := verifyAllAPIServicesAvailable().(verifyAllAPIServicesAvailableImpl)
	if !ok {
		t.Fatalf("expected verifyAllAPIServicesAvailableImpl, got %T", verifyAllAPIServicesAvailable())
	}
	if verifier.timeout <= 0 {
		t.Fatalf("verifier timeout must be > 0, got %s", verifier.timeout)
	}
}

func TestVerifyAllAPIServicesAvailable_CheckOnce(t *testing.T) {
	available := availableCondition(apiregistrationv1.ConditionTrue, "all checks passed")

	for _, tc := range []struct {
		name        string
		items       []apiregistrationv1.APIService
		listErr     error
		wantErr     bool
		wantInError []string
	}{
		{
			name: "all available",
			items: []apiregistrationv1.APIService{
				apiService("v1.route.openshift.io", available),
				apiService("v1beta1.metrics.k8s.io", available),
			},
		},
		{
			// The ARO-30581 regression: metrics-server is still rolling out, so the
			// aggregator cannot verify its serving certificate yet.
			name: "metrics apiservice not available reports the condition message",
			items: []apiregistrationv1.APIService{
				apiService("v1.route.openshift.io", available),
				apiService("v1beta1.metrics.k8s.io", availableCondition(apiregistrationv1.ConditionFalse,
					`failing or missing response from https://10.129.0.21:10250/apis/metrics.k8s.io/v1beta1: x509: certificate signed by unknown authority`)),
			},
			wantErr: true,
			wantInError: []string{
				"apiservice/v1beta1.metrics.k8s.io is not available because:",
				"x509: certificate signed by unknown authority",
			},
		},
		{
			name: "apiservice without an Available condition",
			items: []apiregistrationv1.APIService{
				apiService("v1.route.openshift.io", available),
				apiService("v1beta1.metrics.k8s.io"),
			},
			wantErr:     true,
			wantInError: []string{"apiservice/v1beta1.metrics.k8s.io is not available because it has no status"},
		},
		{
			name: "required apiservice missing",
			items: []apiregistrationv1.APIService{
				apiService("v1beta1.metrics.k8s.io", available),
			},
			wantErr:     true,
			wantInError: []string{"required apiservices are missing: v1.route.openshift.io"},
		},
		{
			// A failed list is surfaced rather than swallowed. pollUntilReady treats it as
			// retryable, which is what we want for transient konnectivity and apiserver
			// errors during rollout.
			name:        "list failure is reported",
			listErr:     errors.New("connection refused"),
			wantErr:     true,
			wantInError: []string{"failed to list all APIServices", "connection refused"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// checkOnce performs a single attempt and does not read the timeout.
			verifier := verifyAllAPIServicesAvailableImpl{}

			err := verifier.checkOnce(context.Background(), fakeAPIServiceLister{items: tc.items, err: tc.listErr})

			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected success, got: %v", err)
				}
				return
			}
			for _, want := range tc.wantInError {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected error to contain %q, got: %v", want, err)
				}
			}
		})
	}
}
