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

package clients

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestGetRemoteOptionsUsesDockerConfig(t *testing.T) {
	const (
		username = "registry-user"
		password = "registry-password"
	)
	expectedAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","size":0},"layers":[]}`)
	manifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifest))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expectedAuthorization {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", manifestDigest)
		_, _ = w.Write(manifest)
	}))
	defer server.Close()

	registryHost := strings.TrimPrefix(server.URL, "http://")
	dockerConfigDir := t.TempDir()
	dockerConfig := fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, registryHost, base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	if err := os.WriteFile(filepath.Join(dockerConfigDir, "config.json"), []byte(dockerConfig), 0600); err != nil {
		t.Fatalf("failed to write Docker config: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", dockerConfigDir)

	ref, err := name.ParseReference(registryHost+"/test:latest", name.Insecure)
	if err != nil {
		t.Fatalf("failed to parse test registry reference: %v", err)
	}
	options := append(GetRemoteOptions(true), remote.WithTransport(server.Client().Transport))
	if _, err := remote.Get(ref, options...); err != nil {
		t.Fatalf("authenticated registry request failed: %v", err)
	}
}
