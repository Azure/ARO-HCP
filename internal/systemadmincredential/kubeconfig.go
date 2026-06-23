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

	"k8s.io/apimachinery/pkg/runtime"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clientcmdapilatest "k8s.io/client-go/tools/clientcmd/api/latest"
)

// BuildKubeconfigInput is the strict input set BuildKubeconfig consumes.
// Treat as immutable; the function does not mutate any field.
type BuildKubeconfigInput struct {
	// APIURL is the cluster's API server URL (https://<host>:<port>).
	// Sourced from HCPOpenShiftCluster.ServiceProviderProperties.API.URL
	// by the caller — see PLAN.md's "Where the parent cluster's CA and
	// API URL come from" subsection.
	APIURL string
	// ServingCABundle is the PEM-encoded API server serving CA bundle.
	// Sourced from ServiceProviderCluster.Status.ServingCABundle.
	ServingCABundle []byte
	// SignedCertificatePEM is the PEM-encoded user certificate the
	// HyperShift signer produced. Stored in
	// SystemAdminCredentialStatus.SignedCertificate as base64 of the
	// PEM bytes — the caller decodes before calling.
	SignedCertificatePEM []byte
	// PrivateKeyPEM is the PEM-encoded RSA private key matching
	// SignedCertificatePEM. Read straight off
	// SystemAdminCredentialSpec.PrivateKeyPEM by the caller.
	PrivateKeyPEM []byte
	// Username is the kubeconfig context user name. Mirrors the
	// username embedded in the cert CN.
	Username string
	// ClusterName is the kubeconfig "clusters" entry name. Convention
	// is the customer-visible cluster name, but any non-empty string
	// works — kubeconfigs are self-referential.
	ClusterName string
}

// DecodeBase64Cert is a small convenience for callers that pull
// Status.SignedCertificate (base64-of-PEM) off Cosmos and need PEM bytes
// for BuildKubeconfig. Returns a wrapped error if the input is not
// valid base64.
func DecodeBase64Cert(b64 string) ([]byte, error) {
	out, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decoding base64 signed certificate: %w", err)
	}
	return out, nil
}

// BuildKubeconfig assembles a kubeconfig YAML body suitable for return
// to a customer via the OperationResult endpoint. No I/O; the function
// is deterministic in its inputs and safe to call from a request
// handler.
func BuildKubeconfig(in BuildKubeconfigInput) ([]byte, error) {
	switch {
	case in.APIURL == "":
		return nil, fmt.Errorf("BuildKubeconfig: APIURL must not be empty")
	case len(in.ServingCABundle) == 0:
		return nil, fmt.Errorf("BuildKubeconfig: ServingCABundle must not be empty")
	case len(in.SignedCertificatePEM) == 0:
		return nil, fmt.Errorf("BuildKubeconfig: SignedCertificatePEM must not be empty")
	case len(in.PrivateKeyPEM) == 0:
		return nil, fmt.Errorf("BuildKubeconfig: PrivateKeyPEM must not be empty")
	case in.Username == "":
		return nil, fmt.Errorf("BuildKubeconfig: Username must not be empty")
	case in.ClusterName == "":
		return nil, fmt.Errorf("BuildKubeconfig: ClusterName must not be empty")
	}

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[in.ClusterName] = &clientcmdapi.Cluster{
		Server:                   in.APIURL,
		CertificateAuthorityData: append([]byte(nil), in.ServingCABundle...),
	}
	cfg.AuthInfos[in.Username] = &clientcmdapi.AuthInfo{
		ClientCertificateData: append([]byte(nil), in.SignedCertificatePEM...),
		ClientKeyData:         append([]byte(nil), in.PrivateKeyPEM...),
	}
	contextName := in.Username + "@" + in.ClusterName
	cfg.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  in.ClusterName,
		AuthInfo: in.Username,
	}
	cfg.CurrentContext = contextName

	out, err := runtime.Encode(clientcmdapilatest.Codec, cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding kubeconfig: %w", err)
	}
	return out, nil
}
