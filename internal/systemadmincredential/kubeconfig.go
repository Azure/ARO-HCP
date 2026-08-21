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

// BuildKubeconfig assembles a kubeconfig from the signed certificate, the API
// URL, and an optional CA bundle. The private key is NOT included — the caller
// holds it and must inject it into the kubeconfig before use.
//
// signedCertificateBase64 is the base64 encoding of the CSR's Status.Certificate,
// which the Kubernetes API guarantees to be PEM-encoded. client-go's clientcmd
// expects ClientCertificateData in PEM, so the decoded bytes are used directly
// (no DER→PEM wrapping is required).
//
// caBundlePEM is the PEM-encoded serving CA certificate for the API server. When
// non-empty it is set as CertificateAuthorityData so kubectl can verify the TLS
// connection. When empty, callers fall back to their system trust bundle.
func BuildKubeconfig(signedCertificateBase64, apiURL, caBundlePEM string) ([]byte, error) {
	certPEM, err := base64.StdEncoding.DecodeString(signedCertificateBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signed certificate: %w", err)
	}

	var caData []byte
	if len(caBundlePEM) > 0 {
		caData = []byte(caBundlePEM)
	}

	config := clientcmdapi.NewConfig()
	config.Clusters[kubeconfigClusterName] = &clientcmdapi.Cluster{
		Server:                   apiURL,
		CertificateAuthorityData: caData,
	}
	config.AuthInfos[kubeconfigUserName] = &clientcmdapi.AuthInfo{
		ClientCertificateData: certPEM,
	}
	config.Contexts[kubeconfigContextName] = &clientcmdapi.Context{
		Cluster:  kubeconfigClusterName,
		AuthInfo: kubeconfigUserName,
	}
	config.CurrentContext = kubeconfigContextName

	return clientcmd.Write(*config)
}
