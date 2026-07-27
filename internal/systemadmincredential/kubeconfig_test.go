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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/clientcmd"
)

func TestBuildKubeconfig(t *testing.T) {
	fakeCert := base64.StdEncoding.EncodeToString([]byte("fake-cert-data"))
	fakeURL := "https://api.cluster.example.com:6443"

	t.Run("without CA bundle", func(t *testing.T) {
		kubeconfigBytes, err := BuildKubeconfig(fakeCert, fakeURL, "")
		require.NoError(t, err, "BuildKubeconfig should succeed")

		config, err := clientcmd.Load(kubeconfigBytes)
		require.NoError(t, err, "kubeconfig should be parseable")

		assert.Equal(t, kubeconfigContextName, config.CurrentContext, "current context should be set")

		cluster, ok := config.Clusters[kubeconfigClusterName]
		require.True(t, ok, "cluster should exist in kubeconfig")
		assert.Equal(t, fakeURL, cluster.Server, "cluster server should match API URL")
		assert.Empty(t, cluster.CertificateAuthorityData, "cluster CA should be empty when no CA bundle is provided")

		authInfo, ok := config.AuthInfos[kubeconfigUserName]
		require.True(t, ok, "user should exist in kubeconfig")
		assert.Empty(t, authInfo.ClientKeyData, "client key should not be present")
		assert.Equal(t, []byte("fake-cert-data"), authInfo.ClientCertificateData, "client cert should match decoded cert")

		ctx, ok := config.Contexts[kubeconfigContextName]
		require.True(t, ok, "context should exist in kubeconfig")
		assert.Equal(t, kubeconfigClusterName, ctx.Cluster, "context cluster should match")
		assert.Equal(t, kubeconfigUserName, ctx.AuthInfo, "context user should match")
	})

	t.Run("with CA bundle", func(t *testing.T) {
		fakeCA := "-----BEGIN CERTIFICATE-----\nfake-ca-data\n-----END CERTIFICATE-----"
		kubeconfigBytes, err := BuildKubeconfig(fakeCert, fakeURL, fakeCA)
		require.NoError(t, err, "BuildKubeconfig should succeed")

		config, err := clientcmd.Load(kubeconfigBytes)
		require.NoError(t, err, "kubeconfig should be parseable")

		cluster, ok := config.Clusters[kubeconfigClusterName]
		require.True(t, ok, "cluster should exist in kubeconfig")
		assert.Equal(t, []byte(fakeCA), cluster.CertificateAuthorityData, "cluster CA should match provided CA bundle")
	})
}
