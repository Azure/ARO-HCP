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

package systemadmincredential

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	certificatesv1 "k8s.io/api/certificates/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestBuildCSR(t *testing.T) {
	_, privPEM, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error = %v", err)
	}

	ownerID, err := azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1")
	if err != nil {
		t.Fatalf("ParseResourceID() error = %v", err)
	}

	csr, err := BuildCSR(ownerID, "abcdef1234567890", "clusters-cluster1", "system-admin", privPEM)
	if err != nil {
		t.Fatalf("BuildCSR() error = %v", err)
	}

	// Verify name
	expectedName := "system-admin-credential-abcdef1234567890"
	if csr.Name != expectedName {
		t.Errorf("CSR name = %q, want %q", csr.Name, expectedName)
	}

	// Verify signer name
	expectedSigner := "hypershift.openshift.io/clusters-cluster1.customer-break-glass"
	if csr.Spec.SignerName != expectedSigner {
		t.Errorf("CSR signer = %q, want %q", csr.Spec.SignerName, expectedSigner)
	}

	// Verify usages
	if len(csr.Spec.Usages) != 1 || csr.Spec.Usages[0] != certificatesv1.UsageClientAuth {
		t.Errorf("CSR usages = %v, want [ClientAuth]", csr.Spec.Usages)
	}

	// Verify expiration
	if csr.Spec.ExpirationSeconds == nil || *csr.Spec.ExpirationSeconds != 86400 {
		t.Errorf("CSR expiration = %v, want 86400", csr.Spec.ExpirationSeconds)
	}

	// Verify owner annotation
	if csr.Annotations[OwnerAnnotationKey] == "" {
		t.Error("CSR missing owner annotation")
	}

	// Verify the CSR request is valid PKCS#10
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil {
		t.Fatal("failed to decode CSR request PEM")
	}
	parsedCSR, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse CSR request: %v", err)
	}
	if parsedCSR.Subject.CommonName != "system-admin" {
		t.Errorf("CSR CN = %q, want %q", parsedCSR.Subject.CommonName, "system-admin")
	}
}

func TestBuildCSR_NilOwnerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("BuildCSR with nil owner should panic")
		}
	}()

	_, privPEM, _ := GenerateKeypair()
	_, _ = BuildCSR(nil, "cred", "ns", "user", privPEM)
}
