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

// Package sharedingress gates ManagementCluster readiness on shared-ingress
// availability. It contains two controllers: EnsureSharedIngressReadDesire
// ensures the shared-ingress ReadDesire exists, and SharedIngressReporting
// mirrors the router Service's load balancer IPs onto the ManagementCluster
// status and toggles the SharedIngressAvailable condition.
package sharedingress

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
)

// ReadDesireName is the name of the management-cluster-scoped ReadDesire that
// mirrors the shared-ingress router Service.
const ReadDesireName = "sharedingress"

// SharedIngressTarget is the shared-ingress router Service mirrored by the
// ReadDesire: a namespaced Service named "router" in the
// "hypershift-sharedingress" namespace on each management cluster.
var SharedIngressTarget = kubeapplierapi.ResourceReference{
	Group:     "",
	Version:   "v1",
	Resource:  "services",
	Namespace: "hypershift-sharedingress",
	Name:      "router",
}

// GetSharedIngressService reads and unmarshals the shared-ingress router
// Service from the ReadDesire lister. It returns (nil, nil) when the ReadDesire
// exists but its content has not been mirrored yet.
func GetSharedIngressService(ctx context.Context, readDesireLister kubeapplierlisters.ReadDesireLister, stampIdentifier string) (*corev1.Service, error) {
	readDesire, err := readDesireLister.GetForManagementCluster(ctx, stampIdentifier, ReadDesireName)
	if err != nil {
		return nil, err
	}
	if readDesire.Status.KubeContent == nil {
		return nil, nil
	}
	var service corev1.Service
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, &service); err != nil {
		return nil, fmt.Errorf("failed to unmarshal shared ingress Service: %w", err)
	}
	return &service, nil
}
