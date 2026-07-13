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

// BuildKubeconfig assembles a kubeconfig from the signed certificate, the private
// key (PEM), and the API URL. This is a pure function — no I/O.
//
// signedCertificateBase64 is the base64 encoding of the CSR's Status.Certificate,
// which the Kubernetes API guarantees to be PEM-encoded. client-go's clientcmd
// expects ClientCertificateData in PEM, so the decoded bytes are used directly
// (no DER→PEM wrapping is required).
//
// The resulting kubeconfig deliberately carries no CertificateAuthorityData. A
// HyperShift shortcoming currently prevents the mirrored serving CA bundle from
// being usable here, so the CA bundle is always nil and callers must fall back to
// their system trust bundle to verify the API server. The serving CA
// ReadDesire (ServingCAReadDesireCreator) and the
// ServiceProviderCluster.Status.ServingCABundle field are intentionally kept —
// but left unused for kubeconfig assembly — so they are ready for future use once
// that HyperShift shortcoming is resolved.
func BuildKubeconfig(signedCertificateBase64, privateKeyPEM, apiURL string) ([]byte, error) {
	certPEM, err := base64.StdEncoding.DecodeString(signedCertificateBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signed certificate: %w", err)
	}

	config := clientcmdapi.NewConfig()
	config.Clusters[kubeconfigClusterName] = &clientcmdapi.Cluster{
		Server: apiURL,
		// CA bundle is intentionally nil — callers must use system trust bundles.
		// The serving CA ReadDesire and ServiceProviderCluster field are maintained
		// for future use once a HyperShift shortcoming is resolved.
		CertificateAuthorityData: nil,
	}
	config.AuthInfos[kubeconfigUserName] = &clientcmdapi.AuthInfo{
		ClientCertificateData: certPEM,
		ClientKeyData:         []byte(privateKeyPEM),
	}
	config.Contexts[kubeconfigContextName] = &clientcmdapi.Context{
		Cluster:  kubeconfigClusterName,
		AuthInfo: kubeconfigUserName,
	}
	config.CurrentContext = kubeconfigContextName

	return clientcmd.Write(*config)
}
