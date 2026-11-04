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

package framework

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// verifyPebbleServingSecret checks that the stored serving certificate pairs
// with its key and chains to caBundle for Pebble's Service host.
func verifyPebbleServingSecret(t *testing.T, secret *corev1.Secret, caBundle []byte) {
	t.Helper()
	_, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	require.NoError(t, err, "serving cert and key must form a usable pair")

	roots, err := parsePEMCertPool(caBundle)
	require.NoError(t, err)
	block, _ := pem.Decode(secret.Data[corev1.TLSCertKey])
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: PebbleServiceHost, Roots: roots, CurrentTime: time.Now()})
	require.NoError(t, err, "serving cert must verify for %s against the returned CA bundle", PebbleServiceHost)
}

func TestPebbleDirectoryURLMatchesServingCertificate(t *testing.T) {
	require.Equal(t, "pebble.pebble.svc", PebbleServiceHost)
	require.Equal(t, "https://pebble.pebble.svc:14000/dir", PebbleDirectoryURL)
}

func TestPebbleDNSServer(t *testing.T) {
	require.Equal(t, "ns1-01.azure-dns.com.:53", pebbleDNSServer("ns1-01.azure-dns.com."))
	require.Equal(t, "ns1-01.azure-dns.com.:53", pebbleDNSServer("ns1-01.azure-dns.com"))
}

func TestPebbleConfigJSON(t *testing.T) {
	data, err := pebbleConfigJSON()
	require.NoError(t, err)
	var config struct {
		Pebble struct {
			ListenAddress           string
			ManagementListenAddress string
			Certificate             string
			PrivateKey              string
		}
	}
	require.NoError(t, json.Unmarshal(data, &config))
	require.Equal(t, "0.0.0.0:14000", config.Pebble.ListenAddress)
	require.Equal(t, "0.0.0.0:15000", config.Pebble.ManagementListenAddress)
	require.Equal(t, "/etc/pebble/tls/tls.crt", config.Pebble.Certificate)
	require.Equal(t, "/etc/pebble/tls/tls.key", config.Pebble.PrivateKey)
}

func TestDeployPebble(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	caBundle, err := DeployPebble(ctx, client, "ns1-01.azure-dns.com.")
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets(PebbleNamespace).Get(ctx, pebbleTLSSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeTLS, secret.Type)
	require.Equal(t, caBundle, secret.Data[corev1.ServiceAccountRootCAKey], "serving CA must be persisted in the Secret")
	verifyPebbleServingSecret(t, secret, caBundle)

	deployment, err := client.AppsV1().Deployments(PebbleNamespace).Get(ctx, pebbleName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	container := deployment.Spec.Template.Spec.Containers[0]
	require.Equal(t, PebbleImage, container.Image)
	require.Contains(t, container.Image, "@sha256:", "Pebble image must be pinned by digest")
	require.Equal(t, []string{"-config", "/etc/pebble/config/pebble-config.json", "-dnsserver", "ns1-01.azure-dns.com.:53"}, container.Args)
	require.Equal(t, int32(1), *deployment.Spec.Replicas)

	service, err := client.CoreV1().Services(PebbleNamespace).Get(ctx, pebbleName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, deployment.Spec.Selector.MatchLabels, service.Spec.Selector)
}

func TestDeployPebbleRedeployReturnsPersistedCA(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	first, err := DeployPebble(ctx, client, "ns1-01.azure-dns.com.")
	require.NoError(t, err)
	second, err := DeployPebble(ctx, client, "ns1-01.azure-dns.com.")
	require.NoError(t, err)
	require.Equal(t, first, second, "redeploy must return the CA of the retained Secret, not a new one")

	secret, err := client.CoreV1().Secrets(PebbleNamespace).Get(ctx, pebbleTLSSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	verifyPebbleServingSecret(t, secret, second)
}

func TestDeployPebbleRejectsExistingSecretWithoutCA(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: pebbleTLSSecretName, Namespace: PebbleNamespace},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("cert"), corev1.TLSPrivateKeyKey: []byte("key")},
	})
	_, err := DeployPebble(ctx, client, "ns1-01.azure-dns.com.")
	require.ErrorContains(t, err, "has no ca.crt")
}

func TestParsePEMCertPoolRejectsEmptyInput(t *testing.T) {
	_, err := parsePEMCertPool([]byte("not pem"))
	require.Error(t, err)
}
