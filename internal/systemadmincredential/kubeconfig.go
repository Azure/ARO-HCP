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

package systemadmincredential

import (
	"encoding/base64"
	"fmt"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	kubeconfigClusterName = "cluster"
	kubeconfigUserName    = "admin"
	kubeconfigContextName = "admin"
)

// BuildKubeconfig assembles a kubeconfig from the signed certificate (base64-DER),
// the private key (PEM), the cluster's serving CA bundle (PEM), and the API URL.
// This is a pure function — no I/O.
func BuildKubeconfig(signedCertificateBase64, privateKeyPEM, servingCABundlePEM, apiURL string) ([]byte, error) {
	certDER, err := base64.StdEncoding.DecodeString(signedCertificateBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signed certificate: %w", err)
	}

	config := clientcmdapi.NewConfig()
	config.Clusters[kubeconfigClusterName] = &clientcmdapi.Cluster{
		Server:                   apiURL,
		CertificateAuthorityData: []byte(servingCABundlePEM),
	}
	config.AuthInfos[kubeconfigUserName] = &clientcmdapi.AuthInfo{
		ClientCertificateData: certDER,
		ClientKeyData:         []byte(privateKeyPEM),
	}
	config.Contexts[kubeconfigContextName] = &clientcmdapi.Context{
		Cluster:  kubeconfigClusterName,
		AuthInfo: kubeconfigUserName,
	}
	config.CurrentContext = kubeconfigContextName

	return clientcmd.Write(*config)
}
