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

package prow

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	github "github.com/Azure/ARO-HCP/tooling/github"
)

const (
	recoveryReserved = "<!-- aro-hcp-prow-recovery"
	recoveryPrefix   = recoveryReserved + ":"
	maxCommentBytes  = 65536
)

// recoveryMetadata preserves the original head, exact targets and seen run IDs.
// The private versioned payload is encoded to keep all values inert to Prow.
type recoveryMetadata struct {
	Version int              `json:"v"`
	Head    string           `json:"h"`
	Targets []recoveryTarget `json:"t"`
	Seen    []string         `json:"s"`
}

// recoveryTarget binds one context to a canonical bucket/job.
type recoveryTarget struct {
	Context string `json:"c"`
	Bucket  string `json:"b"`
	Job     string `json:"j"`
}

// Builds deterministic metadata retaining prior IDs only for targeted jobs.
func newRecoveryMetadata(head string, snapshot github.Snapshot, targets map[string]bool) recoveryMetadata {
	keys := make([]string, 0, len(targets))
	jobs := make(map[string]bool, len(targets))
	for target := range targets {
		keys = append(keys, target)
	}
	sort.Strings(keys)
	metadata := recoveryMetadata{Version: 1, Head: head, Targets: make([]recoveryTarget, 0, len(keys))}
	for _, key := range keys {
		context, job, _ := strings.Cut(key, "\x00")
		bucket, canonical, _ := strings.Cut(job, "/")
		metadata.Targets = append(metadata.Targets, recoveryTarget{Context: context, Bucket: bucket, Job: canonical})
		jobs[job] = true
	}
	baseline := map[string]bool{}
	for _, run := range runs(snapshot) {
		if jobs[run.job] {
			baseline[run.identity] = true
		}
	}
	metadata.Seen = make([]string, 0, len(baseline))
	for identity := range baseline {
		metadata.Seen = append(metadata.Seen, identity)
	}
	sort.Strings(metadata.Seen)
	return metadata
}

// Encodes canonical JSON with an alphabet that cannot introduce Markdown or commands.
func encodeRecoveryMetadata(metadata recoveryMetadata) (string, error) {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// Reconstructs observation state solely from the original durable payload.
func (metadata recoveryMetadata) observation() (map[string]bool, map[string]bool) {
	targets := make(map[string]bool, len(metadata.Targets))
	baseline := make(map[string]bool, len(metadata.Seen))
	for _, target := range metadata.Targets {
		targets[target.Context+"\x00"+target.Bucket+"/"+target.Job] = true
	}
	for _, identity := range metadata.Seen {
		baseline[identity] = true
	}
	return targets, baseline
}

// Decodes one canonical, head-bound payload only from an already trusted comment.
// Absence identifies legacy history; malformed or duplicated metadata is an error.
func decodeRecoveryMetadata(body, marker, head, job string) (recoveryMetadata, bool, error) {
	var metadata recoveryMetadata
	if !strings.Contains(body, recoveryReserved) {
		return metadata, false, nil
	}
	invalid := errors.New("malformed or mismatched recovery observation metadata")
	if len(body) > maxCommentBytes || strings.Count(body, recoveryReserved) != 1 {
		return metadata, true, invalid
	}
	_, tail, ok := strings.Cut(body, "\n\n"+recoveryPrefix)
	if !ok || !strings.HasSuffix(tail, " -->\n\n"+marker) {
		return metadata, true, invalid
	}
	encoded := strings.TrimSuffix(tail, " -->\n\n"+marker)
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || json.Unmarshal(payload, &metadata) != nil {
		return metadata, true, invalid
	}
	// Canonical re-encoding rejects duplicate/unknown JSON fields and extra data.
	canonical, err := encodeRecoveryMetadata(metadata)
	if err != nil || canonical != encoded || metadata.Version != 1 || metadata.Head != head || len(metadata.Targets) == 0 {
		return metadata, true, invalid
	}
	previous := ""
	jobs := make(map[string]bool, len(metadata.Targets))
	for _, target := range metadata.Targets {
		key := target.Context + "\x00" + target.Bucket + "/" + target.Job
		probe := prowRun.FindStringSubmatch("https://prow.ci.openshift.org/view/gs/" + target.Bucket + "/logs/" + target.Job + "/1")
		if target.Context == "" || strings.ContainsRune(target.Context, '\x00') || key <= previous || probe == nil || (job != "" && target.Job != job) {
			return metadata, true, invalid
		}
		previous = key
		jobs[target.Bucket+"/"+target.Job] = false
	}
	prior := ""
	for _, identity := range metadata.Seen {
		run := prowRun.FindStringSubmatch("https://prow.ci.openshift.org/view/gs/" + identity)
		if identity <= prior || run == nil {
			return metadata, true, invalid
		}
		key := run[1] + "/" + run[3]
		if _, targeted := jobs[key]; !targeted {
			return metadata, true, invalid
		}
		jobs[key] = true
		prior = identity
	}
	for _, hasBaseline := range jobs {
		if !hasBaseline {
			return metadata, true, invalid
		}
	}
	return metadata, true, nil
}
