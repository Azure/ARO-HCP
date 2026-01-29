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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type verifyOperatorInstalled struct {
	namespace        string
	subscriptionName string
}

func (v verifyOperatorInstalled) Name() string {
	return "VerifyOperatorInstalled"
}

func (v verifyOperatorInstalled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Check if Subscription exists
	subscriptionGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "subscriptions",
	}

	subscription, err := dynamicClient.Resource(subscriptionGVR).Namespace(v.namespace).Get(ctx, v.subscriptionName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get subscription %s/%s: %w", v.namespace, v.subscriptionName, err)
	}

	// Check subscription state
	state, found, err := unstructured.NestedString(subscription.Object, "status", "state")
	if err != nil {
		return fmt.Errorf("failed to get subscription state: %w", err)
	}
	if !found {
		return fmt.Errorf("subscription state not found")
	}
	if state != "AtLatestKnown" {
		return fmt.Errorf("subscription state is %q, expected AtLatestKnown", state)
	}

	// Get InstallPlan reference
	installPlanRef, found, err := unstructured.NestedString(subscription.Object, "status", "installplan", "name")
	if err != nil {
		return fmt.Errorf("failed to get installplan reference: %w", err)
	}
	if !found {
		return fmt.Errorf("installplan reference not found in subscription")
	}

	// Check InstallPlan status
	installPlanGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "installplans",
	}

	installPlan, err := dynamicClient.Resource(installPlanGVR).Namespace(v.namespace).Get(ctx, installPlanRef, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get installplan %s/%s: %w", v.namespace, installPlanRef, err)
	}

	phase, found, err := unstructured.NestedString(installPlan.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get installplan phase: %w", err)
	}
	if !found {
		return fmt.Errorf("installplan phase not found")
	}
	if phase != "Complete" {
		return fmt.Errorf("installplan phase is %q, expected Complete", phase)
	}

	return nil
}

func VerifyOperatorInstalled(namespace, subscriptionName string) HostedClusterVerifier {
	return verifyOperatorInstalled{
		namespace:        namespace,
		subscriptionName: subscriptionName,
	}
}

type verifyOperatorCSV struct {
	namespace string
	csvName   string
}

func (v verifyOperatorCSV) Name() string {
	return "VerifyOperatorCSV"
}

func (v verifyOperatorCSV) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	csvGVR := schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "clusterserviceversions",
	}

	csv, err := dynamicClient.Resource(csvGVR).Namespace(v.namespace).Get(ctx, v.csvName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CSV %s/%s: %w", v.namespace, v.csvName, err)
	}

	phase, found, err := unstructured.NestedString(csv.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get CSV phase: %w", err)
	}
	if !found {
		return fmt.Errorf("CSV phase not found")
	}
	if phase != "Succeeded" {
		return fmt.Errorf("CSV phase is %q, expected Succeeded", phase)
	}

	return nil
}

func VerifyOperatorCSV(namespace, csvName string) HostedClusterVerifier {
	return verifyOperatorCSV{
		namespace: namespace,
		csvName:   csvName,
	}
}
