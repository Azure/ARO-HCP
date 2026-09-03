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

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalazure "github.com/Azure/ARO-HCP/internal/azure"
)

// validBaseFlags returns a BackendRootCmdFlags satisfying every requirement of validate()
// that is unrelated to the mock identity / ARM Permissions Manager identity flags under test.
func validBaseFlags() BackendRootCmdFlags {
	return BackendRootCmdFlags{
		AzureLocation:                           "westus3",
		ClustersServiceURL:                      "http://localhost:8000",
		AzureCosmosDBName:                       "db",
		AzureCosmosDBURL:                        "http://localhost",
		K8sNamespace:                            "ns",
		MaestroSourceEnvironmentIdentifier:      "dev",
		AzureClusterScopedIdentitiesRoleSetName: string(internalazure.RoleDefinitionConfigSetNameDev),
		BackupScheduleCadence:                   "testing",
		BackupScheduleState:                     "Disabled",
	}
}

func withManagedMockFlags(f BackendRootCmdFlags) BackendRootCmdFlags {
	f.InsecureIgnoreUserAzureManagedIdentitiesThatNeedManagedIdentitiesDataplaneAvailableAndUseMock = true
	f.InsecureAzureManagedIdentityMockCertificateBundlePath = "/secrets/mi-mock-cert-bundle"
	f.InsecureAzureManagedIdentityMockTenantID = "tenant-id"
	f.InsecureAzureARMPermissionsManagerIdentityCertificateBundlePath = "/secrets/arm-helper-cert-bundle"
	f.InsecureAzureARMPermissionsManagerIdentityClientID = "arm-helper-client-id"
	f.InsecureAzureARMPermissionsManagerIdentityTenantID = "tenant-id"
	return f
}

func TestValidateMockIdentityClientAndPrincipalID(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(f *BackendRootCmdFlags)
		wantErr string
	}{
		{
			name: "raw values only is valid",
			mutate: func(f *BackendRootCmdFlags) {
				f.InsecureAzureManagedIdentityMockClientID = "client-id"
				f.InsecureAzureManagedIdentityMockServicePrincipalID = "principal-id"
			},
		},
		{
			name: "path values only is valid",
			mutate: func(f *BackendRootCmdFlags) {
				f.InsecureAzureManagedIdentityMockClientIDPath = "/secrets/miMockClientId"
				f.InsecureAzureManagedIdentityMockServicePrincipalIDPath = "/secrets/miMockPrincipalId"
			},
		},
		{
			name:    "neither client id nor client id path is set",
			mutate:  func(f *BackendRootCmdFlags) { f.InsecureAzureManagedIdentityMockServicePrincipalID = "principal-id" },
			wantErr: "one of --insecure-azure-managed-identity-mock-client-id or --insecure-azure-managed-identity-mock-client-id-path must be set",
		},
		{
			name: "both client id and client id path are set",
			mutate: func(f *BackendRootCmdFlags) {
				f.InsecureAzureManagedIdentityMockClientID = "client-id"
				f.InsecureAzureManagedIdentityMockClientIDPath = "/secrets/miMockClientId"
				f.InsecureAzureManagedIdentityMockServicePrincipalID = "principal-id"
			},
			wantErr: "--insecure-azure-managed-identity-mock-client-id and --insecure-azure-managed-identity-mock-client-id-path are mutually exclusive",
		},
		{
			name:    "neither principal id nor principal id path is set",
			mutate:  func(f *BackendRootCmdFlags) { f.InsecureAzureManagedIdentityMockClientID = "client-id" },
			wantErr: "one of --insecure-azure-managed-identity-mock-principal-id or --insecure-azure-managed-identity-mock-principal-id-path must be set",
		},
		{
			name: "both principal id and principal id path are set",
			mutate: func(f *BackendRootCmdFlags) {
				f.InsecureAzureManagedIdentityMockClientID = "client-id"
				f.InsecureAzureManagedIdentityMockServicePrincipalID = "principal-id"
				f.InsecureAzureManagedIdentityMockServicePrincipalIDPath = "/secrets/miMockPrincipalId"
			},
			wantErr: "--insecure-azure-managed-identity-mock-principal-id and --insecure-azure-managed-identity-mock-principal-id-path are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withManagedMockFlags(validBaseFlags())
			tt.mutate(&f)

			err := f.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateMockIdentityNotManagedRejectsPathFlags(t *testing.T) {
	f := validBaseFlags()
	f.InsecureAzureManagedIdentityMockClientIDPath = "/secrets/miMockClientId"

	err := f.validate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "--insecure-azure-managed-identity-mock-client-id-path must not be set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveInsecureManagedIdentityMockIDsFromFiles(t *testing.T) {
	dir := t.TempDir()
	clientIDPath := filepath.Join(dir, "clientId")
	principalIDPath := filepath.Join(dir, "principalId")
	writeFile(t, clientIDPath, "  client-id-value\n")
	writeFile(t, principalIDPath, "principal-id-value\n")

	f := &BackendRootCmdFlags{
		InsecureAzureManagedIdentityMockClientIDPath:           clientIDPath,
		InsecureAzureManagedIdentityMockServicePrincipalIDPath: principalIDPath,
	}

	if err := f.resolveInsecureManagedIdentityMockIDsFromFiles(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.InsecureAzureManagedIdentityMockClientID != "client-id-value" {
		t.Fatalf("expected trimmed client id, got %q", f.InsecureAzureManagedIdentityMockClientID)
	}
	if f.InsecureAzureManagedIdentityMockServicePrincipalID != "principal-id-value" {
		t.Fatalf("expected trimmed principal id, got %q", f.InsecureAzureManagedIdentityMockServicePrincipalID)
	}
}

func TestResolveInsecureManagedIdentityMockIDsFromFilesNoop(t *testing.T) {
	f := &BackendRootCmdFlags{
		InsecureAzureManagedIdentityMockClientID:           "already-set",
		InsecureAzureManagedIdentityMockServicePrincipalID: "already-set",
	}
	if err := f.resolveInsecureManagedIdentityMockIDsFromFiles(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.InsecureAzureManagedIdentityMockClientID != "already-set" {
		t.Fatalf("expected raw value to be left untouched, got %q", f.InsecureAzureManagedIdentityMockClientID)
	}
}

func TestResolveInsecureManagedIdentityMockIDsFromFilesMissingFile(t *testing.T) {
	f := &BackendRootCmdFlags{
		InsecureAzureManagedIdentityMockClientIDPath: filepath.Join(t.TempDir(), "does-not-exist"),
	}
	if err := f.resolveInsecureManagedIdentityMockIDsFromFiles(); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestResolveInsecureManagedIdentityMockIDsFromFilesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty")
	writeFile(t, path, "   \n")

	f := &BackendRootCmdFlags{
		InsecureAzureManagedIdentityMockClientIDPath: path,
	}
	if err := f.resolveInsecureManagedIdentityMockIDsFromFiles(); err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
