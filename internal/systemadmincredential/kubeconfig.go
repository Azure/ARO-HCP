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

package systemadmincredential

import (
	"encoding/base64"
	"fmt"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	kubeconfigClusterName = "cluster"
	kubeconfigContextName = "admin"
)

// BuildKubeconfig assembles a kubeconfig from the given components:
//   - apiURL: the cluster's API server URL
//   - servingCABundlePEM: PEM-encoded CA bundle for the API server
//   - signedCertBase64: base64-encoded signed client certificate (DER)
//   - privateKeyPEM: PEM-encoded private key
//   - username: the K8s username (used as the auth-info name)
//
// The returned bytes are a serialized kubeconfig YAML.
func BuildKubeconfig(apiURL, servingCABundlePEM string, signedCertBase64 string, privateKeyPEM string, username string) ([]byte, error) {
	clientCert, err := base64.StdEncoding.DecodeString(signedCertBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signed certificate: %w", err)
	}

	config := clientcmdapi.NewConfig()
	config.Clusters[kubeconfigClusterName] = &clientcmdapi.Cluster{
		Server:                   apiURL,
		CertificateAuthorityData: []byte(servingCABundlePEM),
	}
	config.AuthInfos[username] = &clientcmdapi.AuthInfo{
		ClientCertificateData: clientCert,
		ClientKeyData:         []byte(privateKeyPEM),
	}
	config.Contexts[kubeconfigContextName] = &clientcmdapi.Context{
		Cluster:  kubeconfigClusterName,
		AuthInfo: username,
	}
	config.CurrentContext = kubeconfigContextName

	return clientcmd.Write(*config)
}
