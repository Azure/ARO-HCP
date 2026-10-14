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

package probe

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "PRIVATE_CA_SUBJECT"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{cert: cert, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func (ca testCA) issue(t *testing.T, name string, expired bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "PRIVATE_LEAF_SUBJECT"}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if expired {
		cert.NotAfter = time.Now().Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func newTLSServer(t *testing.T, config *tls.Config, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = config
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func TestIgnitionTLSVerification(t *testing.T) {
	ignition, root := newTestCA(t), newTestCA(t)
	leaf := ignition.issue(t, "router.test", false)
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}))
	keyDER, err := x509.MarshalECPrivateKey(ignition.key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	for _, tc := range []struct {
		name, serverName, bundle, pem, stage, error string
		expired                                     bool
	}{
		{name: "trusted external", serverName: "router.test", bundle: TrustIgnition, pem: ignition.pem, stage: "complete"},
		{name: "trusted external with leaf", serverName: "router.test", bundle: TrustIgnition, pem: ignition.pem + leafPEM, stage: "complete"},
		{name: "leaf only", serverName: "router.test", bundle: TrustIgnition, pem: leafPEM, stage: "trust", error: "TLS trust unavailable or invalid"},
		{name: "untrusted", serverName: "router.test", bundle: TrustIgnition, pem: root.pem, stage: "tls", error: "TLS verification failed: unknown authority"},
		{name: "wrong bundle cross CA", serverName: "router.test", bundle: TrustRoot, pem: ignition.pem, stage: "tls", error: "TLS verification failed: unknown authority"},
		{name: "wrong name", serverName: "wrong.test", bundle: TrustIgnition, pem: ignition.pem, stage: "tls", error: "TLS verification failed: server name mismatch"},
		{name: "expired", serverName: "router.test", bundle: TrustIgnition, pem: ignition.pem, expired: true, stage: "tls", error: "TLS verification failed: certificate expired or not yet valid"},
		{name: "missing name", bundle: TrustIgnition, pem: ignition.pem, stage: "trust", error: "TLS trust unavailable or invalid"},
		{name: "missing identifier", serverName: "router.test", pem: ignition.pem, stage: "trust", error: "TLS trust unavailable or invalid"},
		{name: "missing bundle", serverName: "router.test", bundle: "absent", pem: ignition.pem, stage: "trust", error: "TLS trust unavailable or invalid"},
		{name: "empty bundle", serverName: "router.test", bundle: TrustIgnition, stage: "trust", error: "TLS trust unavailable or invalid"},
		{name: "corrupt CA", serverName: "router.test", bundle: TrustIgnition, pem: "PRIVATE_CORRUPT_CA", stage: "trust", error: "TLS trust unavailable or invalid"},
		{name: "private key", serverName: "router.test", bundle: TrustIgnition, pem: ignition.pem + privateKey, stage: "trust", error: "TLS trust unavailable or invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := newTLSServer(t, &tls.Config{Certificates: []tls.Certificate{ignition.issue(t, "router.test", tc.expired)}}, func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			})
			target := targetFor(t, server.Listener.Addr().String(), false)
			target.ServerName, target.TrustBundle = tc.serverName, tc.bundle
			req := Request{Targets: []Target{target}, TrustBundles: map[string]string{TrustIgnition: tc.pem, TrustRoot: root.pem}}
			if err := validateRequest(req); err != nil {
				t.Fatalf("trust failure rejected entire request: %v", err)
			}
			pools := loadTrustBundles(req.TrustBundles)
			result := probeTarget(target, pools[target.TrustBundle], time.Second)
			if result.Stage != tc.stage || result.Error != tc.error || !result.TLSVerify || result.TLSVerified != (tc.error == "") {
				t.Fatalf("unexpected verification: %+v", result)
			}
			if (hits.Load() == 1) != (tc.error == "") {
				t.Fatal("HTTP sent without verified TLS, or trusted HTTP not sent")
			}
			if tc.stage == "trust" && (result.Source != "" || result.Timings["connect"] != 0) {
				t.Fatal("missing trust opened a socket")
			}
			var output bytes.Buffer
			if err := writeResult(&output, Result{Targets: []Observation{result}}); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"PRIVATE", "BEGIN CERTIFICATE", "trustBundles", strings.Split(ignition.pem, "\n")[1], strings.Split(privateKey, "\n")[1]} {
				if strings.Contains(output.String(), secret) {
					t.Fatal("certificate or key data leaked into result")
				}
			}
		})
	}
}

func TestTrustBundleParsingAndBounds(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.issue(t, "router.test", false)
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}))
	keyDER, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	ignition := newTestCA(t)
	ignitionPool := loadTrustBundles(map[string]string{TrustIgnition: ignition.pem})[TrustIgnition]
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"valid", ca.pem, true},
		{"multiple CAs", ca.pem + newTestCA(t).pem, true},
		{"whitespace", " \n" + ca.pem + " \n", true},
		{"exact limit", ca.pem + strings.Repeat(" ", maxTrustBundleBytes-len(ca.pem)), true},
		{"over limit", ca.pem + strings.Repeat(" ", maxTrustBundleBytes-len(ca.pem)+1), false},
		{"empty", "", false},
		{"whitespace only", " \n", false},
		{"leaf only", leafPEM, false},
		{"CA with appended ingress leaf", ca.pem + leafPEM, true},
		{"leaf before CA", leafPEM + ca.pem, true},
		{"CA and leaf with private key", ca.pem + leafPEM + privateKey, false},
		{"CA and leaf with malformed PEM", ca.pem + leafPEM + "-----BEGIN CERTIFICATE-----\nBAD\n-----END CERTIFICATE-----\n", false},
		{"CA and leaf with bad DER", ca.pem + leafPEM + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("PRIVATE_BAD_DER")})), false},
		{"garbage prefix", "PRIVATE_JUNK" + ca.pem, false},
		{"garbage suffix", ca.pem + "PRIVATE_JUNK", false},
		{"corrupt prefix block", "-----BEGIN CERTIFICATE-----\nBAD\n-----END CERTIFICATE-----\n" + ca.pem, false},
		{"bad DER", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("PRIVATE_BAD_DER")})), false},
		{"PEM headers", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Private": "data"}, Bytes: ca.cert.Raw})), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools := loadTrustBundles(map[string]string{TrustRoot: tc.input, TrustIgnition: ignition.pem})
			if (pools[TrustRoot] != nil) != tc.valid || !pools[TrustIgnition].Equal(ignitionPool) {
				t.Fatal("wrong bundle acceptance or invalid bundle affected independent trust")
			}
		})
	}
	// Both bundles can use the full PEM budget; identifiers do not count.
	bundles := map[string]string{
		TrustIgnition: ca.pem + strings.Repeat(" ", maxTrustBundleBytes-len(ca.pem)),
		TrustRoot:     ca.pem + strings.Repeat(" ", maxTrustBundleBytes-len(ca.pem)),
	}
	if len(bundles[TrustRoot])+len(bundles[TrustIgnition]) != maxTrustTotalBytes || len(loadTrustBundles(bundles)) != 2 {
		t.Fatal("exact aggregate limit rejected")
	}
	for _, id := range []string{TrustRoot, TrustIgnition, "unknown", strings.Repeat("x", maxTrustTotalBytes+1)} {
		for _, input := range []string{ca.pem, strings.Repeat("x", maxTrustTotalBytes+1)} {
			independent := map[string]string{TrustRoot: bundles[TrustRoot], TrustIgnition: bundles[TrustIgnition]}
			independent[id] = input
			pools := loadTrustBundles(independent)
			for _, known := range []string{TrustRoot, TrustIgnition} {
				want := known != id || len(input) <= maxTrustBundleBytes
				if (pools[known] != nil) != want {
					t.Fatal("rejected bundle suppressed independent valid trust")
				}
			}
			if id != TrustRoot && id != TrustIgnition && pools[id] != nil {
				t.Fatal("unknown identifier accepted")
			}
		}
	}
}

func TestTrustRotationAndInternalService(t *testing.T) {
	oldCA, newCA := newTestCA(t), newTestCA(t)
	const name = "ignition-server.namespace.svc"
	oldCert, newCert := oldCA.issue(t, name, false), newCA.issue(t, name, false)
	ingressCert := oldCA.issue(t, "*.apps.test", false)
	ingressPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ingressCert.Certificate[0]}))
	newLeafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: newCert.Certificate[0]}))
	var current atomic.Pointer[tls.Certificate]
	current.Store(&oldCert)
	server := newTLSServer(t, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return current.Load(), nil }}, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != name || r.TLS.ServerName != name {
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	target := targetFor(t, server.Listener.Addr().String(), false)
	target.ServerName, target.TrustBundle = name, TrustRoot
	for _, tc := range []struct {
		name string
		cert *tls.Certificate
		pem  string
		ok   bool
	}{
		{"old trust", &oldCert, oldCA.pem, true},
		{"root CA with appended ingress leaf", &oldCert, oldCA.pem + ingressPEM, true},
		{"appended leaf is not a trust anchor", &newCert, oldCA.pem + newLeafPEM, false},
		{"rotating overlap old", &oldCert, oldCA.pem + newCA.pem, true},
		{"rotating overlap new", &newCert, oldCA.pem + newCA.pem, true},
		{"stale trust", &newCert, oldCA.pem, false},
		{"new trust", &newCert, newCA.pem, true},
		{"old CA removed", &oldCert, newCA.pem, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current.Store(tc.cert)
			pools := loadTrustBundles(map[string]string{TrustRoot: tc.pem, TrustIgnition: oldCA.pem + newCA.pem})
			result := probeTarget(target, pools[target.TrustBundle], time.Second)
			if result.Expected200 != tc.ok || result.TLSVerified != tc.ok || !result.TLSVerify {
				t.Fatalf("rotation or internal service verification failed: %+v", result)
			}
			if !tc.ok && result.Error != "TLS verification failed: unknown authority" {
				t.Fatalf("wrong rotation failure: %+v", result)
			}
		})
	}
}

func TestTrustAPI(t *testing.T) {
	ca := newTestCA(t)
	target := Target{Address: "127.0.0.1", Port: 443, ServerName: "router.test", TrustBundle: TrustIgnition}
	// Keep Target comparable for discovery's deduplication and result matching.
	targets := map[Target]bool{target: true}
	req := Request{Targets: []Target{target}, TrustBundles: map[string]string{TrustIgnition: ca.pem}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !targets[decoded.Targets[0]] || decoded.TrustBundles[TrustIgnition] != ca.pem || !bytes.Contains(data, []byte(`"trustBundle":"ignition"`)) || !bytes.Contains(data, []byte(`"trustBundles":`)) {
		t.Fatal("trust API round trip failed")
	}
	for _, badID := range []string{ca.pem, "PRIVATE KEY", strings.Repeat("x", 254)} {
		req.Targets[0].TrustBundle = badID
		if err := validateRequest(req); err == nil || strings.Contains(err.Error(), badID) {
			t.Fatal("invalid identifier accepted or echoed")
		}
	}
	result := probeTarget(target, x509.NewCertPool(), time.Second)
	if result.Stage != "trust" || !result.TLSVerify || result.TLSVerified {
		t.Fatalf("empty roots allowed network activity: %+v", result)
	}
}
