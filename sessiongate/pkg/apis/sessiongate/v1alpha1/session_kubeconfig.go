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

package v1alpha1

import (
	"fmt"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func (session *Session) GetKubeconfig(endpoint string) (clientcmdapi.Config, error) {
	if !session.IsReady() {
		return clientcmdapi.Config{}, fmt.Errorf("session is not ready")
	}

	hcpContextName := "hcp"
	hcpClusterName := "hcp"
	authInfoName := "access-token"

	var kubeloginMethod string
	switch session.Spec.Owner.Type {
	case PrincipalTypeAzureUser:
		kubeloginMethod = "azurecli"
	case PrincipalTypeAzureServicePrincipal:
		kubeloginMethod = "spn"
	default:
		return clientcmdapi.Config{}, fmt.Errorf("unexpected identity type: %s", session.Spec.Owner.Type)
	}

	return clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			hcpClusterName: {
				Server: endpoint,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			hcpContextName: {
				Cluster:  hcpClusterName,
				AuthInfo: authInfoName,
			},
		},
		CurrentContext: hcpContextName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			authInfoName: {
				Exec: &clientcmdapi.ExecConfig{
					APIVersion:         "client.authentication.k8s.io/v1beta1",
					Command:            "kubelogin",
					Args:               []string{"get-token", "--login", kubeloginMethod, "--server-id", "6dae42f8-4368-4678-94ff-3960e28e3630"},
					InteractiveMode:    clientcmdapi.IfAvailableExecInteractiveMode,
					ProvideClusterInfo: false,
				},
			},
		},
	}, nil
}
