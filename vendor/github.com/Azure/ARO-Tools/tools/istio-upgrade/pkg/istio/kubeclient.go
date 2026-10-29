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

package istio

import (
	"context"
	"fmt"
	"slices"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type KubeClient struct {
	client       kubernetes.Interface
	mu           sync.RWMutex
	cachedNS     []MeshNamespace
	nsCacheValid bool
}

func NewKubeClient(kubeconfigPath string) (*KubeClient, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return &KubeClient{client: client}, nil
}

func NewKubeClientFromInterface(client kubernetes.Interface) *KubeClient {
	return &KubeClient{client: client}
}

func (k *KubeClient) GetMeshNamespaces(ctx context.Context) ([]MeshNamespace, error) {
	k.mu.RLock()
	if k.nsCacheValid {
		ns := slices.Clone(k.cachedNS)
		k.mu.RUnlock()
		return ns, nil
	}
	k.mu.RUnlock()

	k.mu.Lock()
	defer k.mu.Unlock()
	// Re-check after acquiring write lock.
	if k.nsCacheValid {
		return slices.Clone(k.cachedNS), nil
	}
	nsList, err := k.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "istio.io/rev",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list mesh namespaces: %w", err)
	}
	var namespaces []MeshNamespace
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, MeshNamespace{
			Name:          ns.Name,
			RevisionLabel: ns.Labels["istio.io/rev"],
		})
	}
	k.cachedNS = namespaces
	k.nsCacheValid = true
	return slices.Clone(namespaces), nil
}

func (k *KubeClient) InvalidateNamespaceCache() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cachedNS = nil
	k.nsCacheValid = false
}
