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

package cijoboutcomes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

type prowCompletion struct {
	Result     string
	FinishedAt time.Time
}

// fetchProwCompletion reads only the root finished.json. Missing or invalid
// completion records remain pending; the caller enforces the artifact grace period.
func fetchProwCompletion(ctx context.Context, client *http.Client, jobURI string) (*prowCompletion, error) {
	prowURL := jobURI
	if strings.HasPrefix(jobURI, "gs://") {
		prowURL = "https://prow.ci.openshift.org/view/gs/" + strings.TrimPrefix(jobURI, "gs://")
	}
	info, err := snapshot.ParseProwURL(prowURL)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf(finishedJSONURL, info.GCSBucket, info.GCSPrefix)
	var finished prowFinished
	if err := fetchJSONArtifact(ctx, client, url, &finished); err != nil {
		return nil, enrichmentError(ctx, err)
	}
	// Sidecar writes uppercase verdicts, while Crier can write lowercase states.
	// Keep stored outcomes canonical without inferring them from the passed flag.
	result := strings.ToUpper(finished.Result)
	switch result {
	case "SUCCESS", "FAILURE", "ABORTED", "ERROR":
	default:
		return nil, enrichmentError(ctx, fmt.Errorf("%w: invalid result %q in %s", errArtifactMalformed, finished.Result, url))
	}
	if finished.Timestamp <= 0 || finished.Timestamp > 253402300799 {
		return nil, enrichmentError(ctx, fmt.Errorf("%w: invalid timestamp %d in %s", errArtifactMalformed, finished.Timestamp, url))
	}
	return &prowCompletion{Result: result, FinishedAt: time.Unix(finished.Timestamp, 0).UTC()}, nil
}

// Read prowjob.json once for both annotations and the preferred start time.
// A malformed start time must not discard otherwise valid annotations.
func fetchProwRunMetadata(ctx context.Context, client *http.Client, bucket, prefix string) (runDetail, error) {
	var detail runDetail
	var job struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Status struct {
			StartTime json.RawMessage `json:"startTime"`
		} `json:"status"`
	}
	url := fmt.Sprintf("https://storage.googleapis.com/%s/%s/prowjob.json", bucket, prefix)
	err := fetchJSONArtifact(ctx, client, url, &job)
	if err != nil {
		if err := enrichmentError(ctx, err); err != nil {
			return detail, err
		}
	} else {
		detail.ADOBuildID = job.Metadata.Annotations["ev2.rollout/build"]
		if len(job.Status.StartTime) > 0 {
			err = json.Unmarshal(job.Status.StartTime, &detail.StartedAt)
		}
		if err != nil || detail.StartedAt.IsZero() || detail.StartedAt.Unix() <= 0 {
			_ = enrichmentError(ctx, fmt.Errorf("%w: missing or invalid status.startTime in %s", errArtifactMalformed, url))
			detail.StartedAt = time.Time{}
		} else {
			detail.StartedAt = detail.StartedAt.UTC()
			return detail, nil
		}
	}

	var started struct {
		Timestamp int64 `json:"timestamp"`
	}
	url = fmt.Sprintf("https://storage.googleapis.com/%s/%s/started.json", bucket, prefix)
	if err := fetchJSONArtifact(ctx, client, url, &started); err != nil {
		return detail, enrichmentError(ctx, err)
	}
	if started.Timestamp <= 0 || started.Timestamp > 253402300799 {
		return detail, enrichmentError(ctx, fmt.Errorf("%w: missing or invalid timestamp in %s", errArtifactMalformed, url))
	}
	detail.StartedAt = time.Unix(started.Timestamp, 0).UTC()
	return detail, nil
}
