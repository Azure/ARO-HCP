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

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPebbleServingTLSVerifiesForServiceHost(t *testing.T) {
	now := time.Now()
	servingTLS, err := newPebbleServingTLS(now)
	require.NoError(t, err)

	_, err = tls.X509KeyPair(servingTLS.certPEM, servingTLS.keyPEM)
	require.NoError(t, err, "serving cert and key must form a usable pair")

	roots, err := parsePEMCertPool(servingTLS.caPEM)
	require.NoError(t, err)
	block, _ := pem.Decode(servingTLS.certPEM)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	for _, host := range []string{pebbleServiceHost, pebbleServiceHost + ".cluster.local"} {
		_, err = leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, CurrentTime: now})
		require.NoError(t, err, "serving cert must verify for %s", host)
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: "acme-staging-v02.api.letsencrypt.org", Roots: roots, CurrentTime: now})
	require.Error(t, err)
}

func TestPebbleDirectoryURLMatchesServingCertificate(t *testing.T) {
	require.Equal(t, "https://pebble.pebble.svc:14000/dir", pebbleDirectoryURL)
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

	caBundle, err := deployPebble(ctx, client, "ns1-01.azure-dns.com.")
	require.NoError(t, err)
	_, err = parsePEMCertPool(caBundle)
	require.NoError(t, err)

	secret, err := client.CoreV1().Secrets(pebbleNamespace).Get(ctx, pebbleTLSSecretName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeTLS, secret.Type)

	deployment, err := client.AppsV1().Deployments(pebbleNamespace).Get(ctx, pebbleName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	container := deployment.Spec.Template.Spec.Containers[0]
	require.Equal(t, pebbleImage, container.Image)
	require.Contains(t, container.Image, "@sha256:", "Pebble image must be pinned by digest")
	require.Equal(t, []string{"-config", "/etc/pebble/config/pebble-config.json", "-dnsserver", "ns1-01.azure-dns.com.:53"}, container.Args)
	require.Equal(t, int32(1), *deployment.Spec.Replicas)

	service, err := client.CoreV1().Services(pebbleNamespace).Get(ctx, pebbleName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, deployment.Spec.Selector.MatchLabels, service.Spec.Selector)

	// Re-running against an existing install must not fail.
	_, err = deployPebble(ctx, client, "ns1-01.azure-dns.com.")
	require.NoError(t, err)
}

func TestUnstructuredClusterIssuerCarriesCABundle(t *testing.T) {
	caBundle := []byte("-----BEGIN CERTIFICATE-----\nZm9v\n-----END CERTIFICATE-----\n")
	issuer := unstructuredClusterIssuer(ingressCertificateIssuerName, "noreply@redhat.com", pebbleDirectoryURL, caBundle,
		"aro.example.com", "rg", "sub", "client")
	server, _, err := unstructured.NestedString(issuer.Object, "spec", "acme", "server")
	require.NoError(t, err)
	require.Equal(t, pebbleDirectoryURL, server)
	encoded, _, err := unstructured.NestedString(issuer.Object, "spec", "acme", "caBundle")
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.Equal(t, caBundle, decoded)
}

func TestParsePEMCertPoolRejectsEmptyInput(t *testing.T) {
	_, err := parsePEMCertPool([]byte("not pem"))
	require.Error(t, err)
}
