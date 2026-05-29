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

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func testCRD(scope apiextensionsv1.ResourceScope) crdInfo {
	return crdInfo{
		Name:    "objectsets.package-operator.run",
		Plural:  "objectsets",
		Group:   apiGroup,
		Version: "v1alpha1",
		Scope:   scope,
	}
}

func missingResourceErr() error {
	return apierrors.NewNotFound(schema.GroupResource{Group: apiGroup, Resource: "objectsets"}, "objectsets")
}

func dynamicClientReturning(err error) *dynamicfake.FakeDynamicClient {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gvr(testCRD(apiextensionsv1.ClusterScoped)): "ObjectSetList",
		},
	)
	client.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
	return client
}

func TestIsMissingResourceErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"api_not_found", missingResourceErr(), true},
		{
			"dynamic_resource_missing_text",
			errors.New("the server could not find the requested resource"),
			true,
		},
		{"other", errors.New("timeout"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingResourceErr(tc.err); got != tc.want {
				t.Fatalf("isMissingResourceErr(%v)=%t want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestCountCRs_MissingResourceReturnsZero(t *testing.T) {
	got := countCRs(context.Background(), dynamicClientReturning(missingResourceErr()), testCRD(apiextensionsv1.ClusterScoped))
	if got != 0 {
		t.Fatalf("countCRs()=%d want 0 for missing resource", got)
	}
}

func TestCountAllCRs_MissingResourceDoesNotAbort(t *testing.T) {
	got := countAllCRs(context.Background(), dynamicClientReturning(missingResourceErr()), []crdInfo{
		testCRD(apiextensionsv1.ClusterScoped),
		testCRD(apiextensionsv1.NamespaceScoped),
	})
	if got != 0 {
		t.Fatalf("countAllCRs()=%d want 0 for missing resources", got)
	}
}

func TestStripFinalizersForCRD_MissingResourceIsWarningOnly(t *testing.T) {
	got := stripFinalizersForCRD(
		context.Background(),
		dynamicClientReturning(missingResourceErr()),
		testCRD(apiextensionsv1.ClusterScoped),
		[]byte(`{"metadata":{"finalizers":[]}}`),
	)
	if got != 0 {
		t.Fatalf("stripFinalizersForCRD()=%d want 0 for missing resource", got)
	}
}

func TestDeleteCRs_MissingResourceIsWarningOnly(t *testing.T) {
	got := deleteCRs(context.Background(), dynamicClientReturning(missingResourceErr()), []crdInfo{
		testCRD(apiextensionsv1.ClusterScoped),
		testCRD(apiextensionsv1.NamespaceScoped),
	}, time.Second)
	if got != 0 {
		t.Fatalf("deleteCRs()=%d want 0 for missing resources", got)
	}
}
