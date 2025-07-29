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

package kubeclient

// This package exists to provide a RESTClientGetter implementation that can be used by kubectl.

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// RESTClientGetter implements genericclioptions.RESTClientGetter interface
// to provide kubectl with the necessary kubernetes client configuration.
// this adapter allows kubectl to work with an existing REST config.
type RESTClientGetter struct {
	restConfig *rest.Config
	namespace  string
}

// NewRESTClientGetter creates a new RESTClientGetter with the provided REST config and namespace.
func NewRESTClientGetter(restConfig *rest.Config, namespace string) *RESTClientGetter {
	return &RESTClientGetter{
		restConfig: restConfig,
		namespace:  namespace,
	}
}

// ToRESTConfig returns the underlying REST configuration.
func (r *RESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	if r.restConfig == nil {
		return nil, fmt.Errorf("rest config is nil")
	}
	return r.restConfig, nil
}

// ToDiscoveryClient creates a cached discovery client from the REST configuration.
func (r *RESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(r.restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	// cache discovery results for 10 minutes to improve performance
	return memory.NewMemCacheClient(discoveryClient), nil
}

// ToRESTMapper creates a REST mapper for API resource discovery.
func (r *RESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := r.ToDiscoveryClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery client for REST mapper: %w", err)
	}

	return restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient), nil
}

// ToRawKubeConfigLoader returns a ClientConfig that provides access to the configuration.
func (r *RESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return &rawKubeConfigLoader{
		restConfig: r.restConfig,
		namespace:  r.namespace,
	}
}

// rawKubeConfigLoader implements clientcmd.ClientConfig interface
// to provide kubectl with kubeconfig-style access to our REST configuration.
// this is required by kubectl's factory pattern.
type rawKubeConfigLoader struct {
	restConfig *rest.Config
	namespace  string
}

// RawConfig creates a minimal kubeconfig structure from the REST configuration.
func (r *rawKubeConfigLoader) RawConfig() (clientcmdapi.Config, error) {
	// create a minimal kubeconfig structure from our REST config
	// this provides kubectl with the cluster/user/context information it expects
	config := clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {
				Server:                   r.restConfig.Host,
				CertificateAuthorityData: r.restConfig.CAData,
				InsecureSkipTLSVerify:    r.restConfig.Insecure,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Token:                 r.restConfig.BearerToken,
				ClientCertificateData: r.restConfig.CertData,
				ClientKeyData:         r.restConfig.KeyData,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"context": {
				Cluster:   "cluster",
				AuthInfo:  "user",
				Namespace: r.namespace,
			},
		},
		CurrentContext: "context",
	}
	return config, nil
}

// ClientConfig returns the underlying REST configuration.
func (r *rawKubeConfigLoader) ClientConfig() (*rest.Config, error) {
	if r.restConfig == nil {
		return nil, fmt.Errorf("rest config is nil")
	}
	return r.restConfig, nil
}

// Namespace returns the target namespace, defaulting to "default" if not specified.
func (r *rawKubeConfigLoader) Namespace() (string, bool, error) {
	// return the target namespace, defaulting to "default" if not specified
	// the second return value indicates if the namespace was explicitly set
	namespace := r.namespace
	if namespace == "" {
		namespace = "default"
	}
	return namespace, false, nil
}

// ConfigAccess is not used for our in-memory config approach.
func (r *rawKubeConfigLoader) ConfigAccess() clientcmd.ConfigAccess {
	// not used for our in-memory config approach
	// kubectl doesn't require this for port forwarding operations
	return nil
}
