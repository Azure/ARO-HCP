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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// Pebble is Let's Encrypt's ACME test server, recommended for CI instead of
// the shared staging environment:
// https://letsencrypt.org/docs/staging-environment/#continuous-integration-development-testing
//
// Running it inside the guest cluster removes the dependency on
// acme-staging-v02.api.letsencrypt.org, which is subject to maintenance windows
// and outages outside our control, while still exercising the real customer
// path: cert-manager registers an ACME account, presents a DNS-01 TXT record
// in the cluster's Azure DNS zone via workload identity, and Pebble validates
// that record against the zone's authoritative nameserver before issuing.
//
// Pinned by tag and digest. Bump deliberately; do not float.
const pebbleImage = "ghcr.io/letsencrypt/pebble:2.10.1@sha256:ddf230642b1a584f519f32e347de1b05a6e4c1f6c35c1863b33effeab5f78199"

const (
	pebbleNamespace      = "pebble"
	pebbleName           = "pebble"
	pebbleACMEPort       = 14000
	pebbleManagementPort = 15000
	pebbleTLSSecretName  = "pebble-tls"
	pebbleConfigMapName  = "pebble-config"
)

// pebbleServiceHost is the in-cluster name cert-manager uses to reach Pebble,
// and the name Pebble's serving certificate is issued for.
var pebbleServiceHost = fmt.Sprintf("%s.%s.svc", pebbleName, pebbleNamespace)

// pebbleDirectoryURL is the ACME directory cert-manager's ClusterIssuer points at.
var pebbleDirectoryURL = fmt.Sprintf("https://%s:%d/dir", pebbleServiceHost, pebbleACMEPort)

// pebbleServingTLS holds the PEM-encoded material for Pebble's own HTTPS
// listener. The CA is handed to cert-manager as the ClusterIssuer caBundle; it
// is unrelated to the CA Pebble issues ACME certificates from.
type pebbleServingTLS struct {
	caPEM   []byte
	certPEM []byte
	keyPEM  []byte
}

// newPebbleServingTLS creates a short-lived CA and a serving certificate for
// Pebble's in-cluster Service names.
func newPebbleServingTLS(now time.Time) (*pebbleServingTLS, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating Pebble serving CA key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "aro-hcp-e2e pebble serving CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("creating Pebble serving CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("parsing Pebble serving CA certificate: %w", err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating Pebble serving key: %w", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: pebbleServiceHost},
		DNSNames:     []string{pebbleServiceHost, pebbleServiceHost + ".cluster.local"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("creating Pebble serving certificate: %w", err)
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return nil, fmt.Errorf("marshaling Pebble serving key: %w", err)
	}
	return &pebbleServingTLS{
		caPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER}),
	}, nil
}

// pebbleConfigJSON renders Pebble's configuration file. Field names follow
// cmd/pebble/main.go in the pinned release.
func pebbleConfigJSON() ([]byte, error) {
	return json.MarshalIndent(map[string]any{
		"pebble": map[string]any{
			"listenAddress":                  fmt.Sprintf("0.0.0.0:%d", pebbleACMEPort),
			"managementListenAddress":        fmt.Sprintf("0.0.0.0:%d", pebbleManagementPort),
			"certificate":                    "/etc/pebble/tls/" + corev1.TLSCertKey,
			"privateKey":                     "/etc/pebble/tls/" + corev1.TLSPrivateKeyKey,
			"httpPort":                       5002,
			"tlsPort":                        5001,
			"ocspResponderURL":               "",
			"externalAccountBindingRequired": false,
			"keyAlgorithm":                   "ecdsa",
			"retryAfter":                     map[string]any{"authz": 3, "order": 5},
		},
	}, "", "  ")
}

// pebbleDNSServer returns the -dnsserver argument for Pebble. Pebble queries
// this single server (over TCP) for the _acme-challenge TXT record, so pointing
// it at one of the zone's authoritative nameservers avoids recursive-resolver
// caching. The trailing dot keeps the pod's DNS search path out of the lookup.
func pebbleDNSServer(authoritativeNameserver string) string {
	host := authoritativeNameserver
	if len(host) > 0 && host[len(host)-1] != '.' {
		host += "."
	}
	return fmt.Sprintf("%s:53", host)
}

// deployPebble installs Pebble into the guest cluster and returns the CA bundle
// cert-manager needs to trust Pebble's ACME endpoint. It does not wait for
// readiness; see waitForPebbleReady.
func deployPebble(ctx context.Context, kubeClient kubernetes.Interface, authoritativeNameserver string) ([]byte, error) {
	servingTLS, err := newPebbleServingTLS(time.Now())
	if err != nil {
		return nil, err
	}
	config, err := pebbleConfigJSON()
	if err != nil {
		return nil, fmt.Errorf("rendering Pebble config: %w", err)
	}

	podLabels := map[string]string{"app.kubernetes.io/name": pebbleName}
	create := func(kind string, err error) error {
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating Pebble %s: %w", kind, err)
		}
		return nil
	}

	_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: pebbleNamespace},
	}, metav1.CreateOptions{})
	if err := create("Namespace", err); err != nil {
		return nil, err
	}

	_, err = kubeClient.CoreV1().Secrets(pebbleNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: pebbleTLSSecretName},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       servingTLS.certPEM,
			corev1.TLSPrivateKeyKey: servingTLS.keyPEM,
		},
	}, metav1.CreateOptions{})
	if err := create("TLS Secret", err); err != nil {
		return nil, err
	}

	_, err = kubeClient.CoreV1().ConfigMaps(pebbleNamespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: pebbleConfigMapName},
		Data:       map[string]string{"pebble-config.json": string(config)},
	}, metav1.CreateOptions{})
	if err := create("ConfigMap", err); err != nil {
		return nil, err
	}

	_, err = kubeClient.AppsV1().Deployments(pebbleNamespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: pebbleName, Labels: podLabels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			// Pebble keeps its CA, accounts and orders in memory, so a second
			// replica or a surge pod would hand out a different root.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  pebbleName,
						Image: pebbleImage,
						Args: []string{
							"-config", "/etc/pebble/config/pebble-config.json",
							"-dnsserver", pebbleDNSServer(authoritativeNameserver),
						},
						Env: []corev1.EnvVar{
							// Validate as soon as cert-manager accepts the challenge
							// instead of after a random 0-15s delay.
							{Name: "PEBBLE_VA_NOSLEEP", Value: "1"},
							// Pebble rejects 5% of valid nonces by default to exercise
							// client retries; that is not what this test is about.
							{Name: "PEBBLE_WFE_NONCEREJECT", Value: "0"},
						},
						Ports: []corev1.ContainerPort{
							{Name: "acme", ContainerPort: pebbleACMEPort, Protocol: corev1.ProtocolTCP},
							{Name: "management", ContainerPort: pebbleManagementPort, Protocol: corev1.ProtocolTCP},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path:   "/dir",
								Port:   intstr.FromString("acme"),
								Scheme: corev1.URISchemeHTTPS,
							}},
							PeriodSeconds: 2,
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/pebble/config", ReadOnly: true},
							{Name: "tls", MountPath: "/etc/pebble/tls", ReadOnly: true},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: pebbleConfigMapName},
						}}},
						{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: pebbleTLSSecretName,
						}}},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err := create("Deployment", err); err != nil {
		return nil, err
	}

	_, err = kubeClient.CoreV1().Services(pebbleNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: pebbleName, Labels: podLabels},
		Spec: corev1.ServiceSpec{
			Selector: podLabels,
			Ports: []corev1.ServicePort{
				{Name: "acme", Port: pebbleACMEPort, TargetPort: intstr.FromString("acme"), Protocol: corev1.ProtocolTCP},
				{Name: "management", Port: pebbleManagementPort, TargetPort: intstr.FromString("management"), Protocol: corev1.ProtocolTCP},
			},
		},
	}, metav1.CreateOptions{})
	if err := create("Service", err); err != nil {
		return nil, err
	}

	return servingTLS.caPEM, nil
}

// checkPebbleReady reports whether the Pebble Deployment has a ready replica.
func checkPebbleReady(ctx context.Context, kubeClient kubernetes.Interface) error {
	deployment, err := kubeClient.AppsV1().Deployments(pebbleNamespace).Get(ctx, pebbleName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if deployment.Status.ReadyReplicas < 1 {
		return fmt.Errorf("pebble deployment not ready: ready=%d desired=%d", deployment.Status.ReadyReplicas, deployment.Status.Replicas)
	}
	return nil
}

// fetchPebbleIssuanceRoots returns the root CA Pebble issues certificates
// from. Pebble generates it at startup, so it must be read from the running
// instance; the API server's service proxy reaches the management port
// without exposing it outside the cluster.
func fetchPebbleIssuanceRoots(ctx context.Context, kubeClient kubernetes.Interface) (*x509.CertPool, error) {
	body, err := kubeClient.CoreV1().Services(pebbleNamespace).
		ProxyGet("https", pebbleName, fmt.Sprintf("%d", pebbleManagementPort), "roots/0", nil).
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching Pebble root CA via service proxy: %w", err)
	}
	return parsePEMCertPool(body)
}

func parsePEMCertPool(data []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	count := 0
	for rest := data; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing PEM certificate: %w", err)
		}
		pool.AddCert(cert)
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("no PEM certificates found in %d bytes", len(data))
	}
	return pool, nil
}
