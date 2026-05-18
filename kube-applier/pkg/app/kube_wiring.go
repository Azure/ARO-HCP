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

package app

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewKubeconfig loads a *rest.Config from kubeconfigPath if non-empty, falling
// back to client-go's default loading rules (KUBECONFIG env, $HOME/.kube/config,
// in-cluster). The ConfigMap-mounted kubeconfig in a pod resolves to the
// in-cluster service account when kubeconfigPath is empty.
func NewKubeconfig(kubeconfigPath string) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loader.ExplicitPath = kubeconfigPath
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, nil).ClientConfig()
}

// NewDynamicClient returns a dynamic.Interface backed by cfg. Every controller
// in the kube-applier shares this single client; per-instance reflectors then
// scope themselves to a single GVR + name via the ListWatch.
func NewDynamicClient(cfg *rest.Config) (dynamic.Interface, error) {
	return dynamic.NewForConfig(cfg)
}
