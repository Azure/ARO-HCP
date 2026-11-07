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

// Package evidence retrieves public Prow run artifacts for failure investigation.
//
// The exact bucket and job path from the run URL are preserved. Redirects,
// alternate buckets and oversized or partial bodies are refused, and the
// manifest is published only after every required artifact validated.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
)

// Limits preserved from the current fetcher.
const (
	MaxArtifactBytes = 64 << 20
	ChunkBytes       = 64 << 10
	FetchTimeout     = 60 * time.Second
)

// Artifacts are the files required for a usable run record.
var Artifacts = []string{"finished.json", "build-log.txt"}

var (
	bucketPattern  = `[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]`
	segmentPattern = `[A-Za-z0-9][A-Za-z0-9_.-]*`
	prowPattern    = regexp.MustCompile(
		`^https://prow\.ci\.openshift\.org/view/gs/(` + bucketPattern + `)/` +
			`((?:pr-logs/pull/` + segmentPattern + `/[0-9]+|logs)/` + segmentPattern + `/[0-9]+)/?$`)
	storagePattern = regexp.MustCompile(`^https://storage\.googleapis\.com/(` + bucketPattern + `)/`)
)

// Fetch error classes.
var (
	ErrUnsupportedURL = errors.New("unsupported Prow run URL")
	ErrExistingOutput = errors.New("refusing existing artifact output")
	ErrArtifact       = errors.New("artifact retrieval failed")
)

// Fetcher retrieves run artifacts into an empty directory.
type Fetcher interface {
	Fetch(ctx context.Context, prowURL, directory string) (model.EvidenceManifest, error)
}

// HTTPFetcher is the production fetcher.
type HTTPFetcher struct {
	// Client is used as-is; redirects are rejected by the handler below.
	Client *http.Client
	// MaxBytes bounds a single artifact.
	MaxBytes int64
	// Clock supplies the manifest observation time.
	Clock func() time.Time
}

// NewHTTPFetcher returns a fetcher that refuses redirects.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{
		Client: &http.Client{
			Timeout: FetchTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		MaxBytes: MaxArtifactBytes,
		Clock:    time.Now,
	}
}

// ArtifactBase validates a canonical run URL and returns its artifact base.
func ArtifactBase(prowURL string) (string, error) {
	match := prowPattern.FindStringSubmatch(prowURL)
	if match == nil {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedURL, prowURL)
	}
	return "https://storage.googleapis.com/" + match[1] + "/" + match[2] + "/", nil
}

// ResultURL reports whether a value is a trusted artifact or run URL.
func ResultURL(value string) bool {
	if _, err := ArtifactBase(value); err == nil {
		return true
	}
	return storagePattern.MatchString(value) ||
		strings.HasPrefix(value, "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/") ||
		strings.HasPrefix(value, "https://github.com/")
}

// Fetch downloads the required artifacts and publishes a manifest last.
func (f *HTTPFetcher) Fetch(ctx context.Context, prowURL, directory string) (model.EvidenceManifest, error) {
	base, err := ArtifactBase(prowURL)
	if err != nil {
		return model.EvidenceManifest{}, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return model.EvidenceManifest{}, fmt.Errorf("resolve evidence directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return model.EvidenceManifest{}, fmt.Errorf("create evidence directory: %w", err)
	}
	for _, name := range append(append([]string{}, Artifacts...), "manifest.json") {
		if _, err := os.Lstat(filepath.Join(absolute, name)); err == nil {
			return model.EvidenceManifest{}, fmt.Errorf("%w: %s", ErrExistingOutput, filepath.Join(absolute, name))
		}
	}
	limit := f.MaxBytes
	if limit <= 0 {
		limit = MaxArtifactBytes
	}
	clock := f.Clock
	if clock == nil {
		clock = time.Now
	}
	manifest := model.EvidenceManifest{
		Envelope:     schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindEvidenceManifest},
		ProwURL:      prowURL,
		ArtifactBase: base,
	}
	scratch, err := os.MkdirTemp(absolute, ".fetch-prow-")
	if err != nil {
		return model.EvidenceManifest{}, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(scratch)

	for _, name := range Artifacts {
		staged := filepath.Join(scratch, name)
		size, digest, err := f.download(ctx, base+name, staged, limit)
		if err != nil {
			manifest.Missing = append(manifest.Missing, name)
			return manifest, fmt.Errorf("%w: %s: %w", ErrArtifact, name, err)
		}
		manifest.Files = append(manifest.Files, model.EvidenceFile{Name: name, Path: filepath.Join(absolute, name), Bytes: size, Digest: digest})
		if name == "finished.json" {
			result, err := finishedResult(staged)
			if err != nil {
				manifest.Missing = append(manifest.Missing, "finished.json result")
				return manifest, fmt.Errorf("%w: %s: %w", ErrArtifact, name, err)
			}
			manifest.Result = result
		}
	}
	if manifest.Result == "" {
		manifest.Missing = append(manifest.Missing, "finished.json result")
		return manifest, fmt.Errorf("%w: finished.json is required", ErrArtifact)
	}
	manifest.Complete = true
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		return manifest, fmt.Errorf("write manifest: %w", err)
	}
	published := []string{}
	for _, name := range append(append([]string{}, Artifacts...), "manifest.json") {
		destination := filepath.Join(absolute, name)
		if err := link(filepath.Join(scratch, name), destination); err != nil {
			for _, created := range published {
				_ = os.Remove(created)
			}
			return manifest, fmt.Errorf("publish evidence: %w", err)
		}
		published = append(published, destination)
	}
	_ = clock
	return manifest, nil
}

// Streams one artifact with a hard byte bound and length check.
func (f *HTTPFetcher) download(ctx context.Context, location, destination string, limit int64) (int64, string, error) {
	parsed, err := url.Parse(location)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "storage.googleapis.com" {
		return 0, "", fmt.Errorf("%w: refusing non-canonical location %q", ErrArtifact, location)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return 0, "", err
	}
	response, err := f.Client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	expected := int64(-1)
	if header := response.Header.Get("Content-Length"); header != "" {
		var declared int64
		if _, err := fmt.Sscanf(header, "%d", &declared); err != nil || declared < 0 {
			return 0, "", errors.New("invalid Content-Length")
		}
		expected = declared
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	buffer := make([]byte, ChunkBytes)
	var size int64
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			size += int64(read)
			if size > limit {
				file.Close()
				return 0, "", fmt.Errorf("artifact exceeds %d bytes", limit)
			}
			if _, err := file.Write(buffer[:read]); err != nil {
				file.Close()
				return 0, "", err
			}
			hash.Write(buffer[:read])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return 0, "", readErr
		}
	}
	if err := file.Close(); err != nil {
		return 0, "", err
	}
	if expected >= 0 && size != expected {
		return 0, "", fmt.Errorf("incomplete artifact: expected %d, got %d bytes", expected, size)
	}
	if size == 0 {
		return 0, "", errors.New("empty artifact")
	}
	return size, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// Requires a nonblank string result. A FAILURE result is valid
// fetched evidence, not a retrieval error.
// Requires a nonblank string result. A FAILURE result is valid
// fetched evidence, not a retrieval error.
func finishedResult(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var metadata struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(content, &metadata); err != nil {
		return "", fmt.Errorf("decode finished.json: %w", err)
	}
	result, ok := metadata.Result.(string)
	if !ok || strings.TrimSpace(result) == "" {
		return "", errors.New("expected an object with a nonblank string result")
	}
	return result, nil
}

// Publishes a staged file without overwriting an existing one.
func link(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("%w: %s", ErrExistingOutput, destination)
	}
	return os.Link(source, destination)
}
