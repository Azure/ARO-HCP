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

package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"cloud.google.com/go/storage"
	"github.com/go-logr/logr"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type prowArtifactProblemHandlerKey struct{}

// WithProwArtifactProblemHandler installs an optional synchronous observer for
// permanent artifact problems. Handlers must be safe for concurrent calls when
// the context is shared between workers. Transient errors are not reported.
func WithProwArtifactProblemHandler(ctx context.Context, handler func(source, reason string)) context.Context {
	return context.WithValue(ctx, prowArtifactProblemHandlerKey{}, handler)
}

// ReportProwArtifactProblem logs and reports a permanent artifact problem.
// Sources are e2e, timing, config, observability, or job; reasons are absent or
// malformed. Unknown categories are ignored to keep metric labels bounded.
func ReportProwArtifactProblem(ctx context.Context, source, reason string) {
	switch source {
	case "e2e", "timing", "config", "observability", "job":
	default:
		return
	}
	if reason != "absent" && reason != "malformed" {
		return
	}
	logr.FromContextOrDiscard(ctx).Info("Permanent artifact problem", "artifact_source", source, "reason", reason)
	if handler, ok := ctx.Value(prowArtifactProblemHandlerKey{}).(func(string, string)); ok && handler != nil {
		handler(source, reason)
	}
}

func artifactAbsent(err error) bool {
	var apiErr *googleapi.Error
	return errors.Is(err, storage.ErrObjectNotExist) || errors.Is(err, storage.ErrBucketNotExist) ||
		(errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound)
}

func controllerGCSClient(ctx context.Context, client *http.Client) (*storage.Client, error) {
	gcsClient, err := storage.NewClient(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	// Let the controller own retries instead of blocking its worker in the SDK.
	gcsClient.SetRetry(storage.WithPolicy(storage.RetryNever))
	return gcsClient, nil
}

// FetchProwJobTestResultsStrict reads an atomic E2E batch for a controller.
// Missing or malformed primary artifacts yield nil; valid empty reports yield a
// nonnil empty slice. Primary transport errors return nil and an error. Timing
// transport errors return unenriched results and an error so callers can retain
// names independently. Missing/malformed timing remains optional enrichment.
// Snapshot's best-effort reader is unchanged.
func FetchProwJobTestResultsStrict(ctx context.Context, client *http.Client, info *ProwJobInfo) ([]TestResult, error) {
	gcsClient, err := controllerGCSClient(ctx, client)
	if err != nil {
		return nil, err
	}
	defer gcsClient.Close()
	return fetchProwJobTestResults(ctx, gcsClient, info, client)
}

func downloadProwObject(ctx context.Context, gcsClient *storage.Client, strictClient *http.Client, bucket, path string) ([]byte, error) {
	if strictClient == nil {
		return downloadObject(ctx, gcsClient, bucket, path)
	}
	// The SDK reader reopens interrupted bodies even with RetryNever. Direct
	// reads let the controller retry the entire source instead.
	u := url.URL{Scheme: "https", Host: "storage.googleapis.com", Path: "/" + bucket + "/" + path}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := strictClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", path, storage.ErrObjectNotExist)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching %s", response.StatusCode, path)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return data, nil
}

// FetchProwJobArtifact reads a step-relative artifact, sharing snapshot's job
// directory discovery. Missing directories or objects return nil without error.
func FetchProwJobArtifact(ctx context.Context, client *http.Client, info *ProwJobInfo, stepPath string) ([]byte, error) {
	gcsClient, err := controllerGCSClient(ctx, client)
	if err != nil {
		return nil, err
	}
	defer gcsClient.Close()
	artifactDir, err := findArtifactDir(ctx, gcsClient, info.GCSBucket, info.JobName, info.GCSPrefix)
	if err == nil {
		var data []byte
		data, err = downloadProwObject(ctx, gcsClient, client, info.GCSBucket, fmt.Sprintf("%s/artifacts/%s/%s", info.GCSPrefix, artifactDir, stepPath))
		if err == nil {
			return data, nil
		}
	}
	if artifactAbsent(err) {
		logr.FromContextOrDiscard(ctx).Info("Artifact absent", "path", stepPath, "error", err)
		return nil, nil
	}
	return nil, err
}

// FetchProwJobConfigStrict reads PR cluster enrichment without swallowing
// transport errors while trying fallback paths. Missing/malformed config is
// permanent absence; non-PR jobs have no cluster enrichment here.
func FetchProwJobConfigStrict(ctx context.Context, client *http.Client, info *ProwJobInfo) (*ProwJobConfig, error) {
	if !info.IsPullRequest() {
		return nil, nil
	}
	for _, configPath := range prConfigPaths {
		data, err := FetchProwJobArtifact(ctx, client, info, configPath)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		config, err := ParseConfig(data)
		if err != nil {
			logr.FromContextOrDiscard(ctx).Error(err, "Malformed job config", "path", configPath)
			ReportProwArtifactProblem(ctx, "config", "malformed")
			return nil, nil
		}
		return config, nil
	}
	ReportProwArtifactProblem(ctx, "config", "absent")
	return nil, nil
}
