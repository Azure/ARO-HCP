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

// Package cipatterns reconciles immutable failure computations against retained raw CI facts.
package cipatterns

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cifailures"
)

const (
	resultSchemaVersion int64 = 1
	maxReadRows               = 100000
	maxReadBytes              = 64 << 20
	maxRowBytes               = 32 << 20
	maxBatchBytes             = 32 << 20
	maxBatchRows              = 128
)

// A failurePatternVersion is compiled into the binary and is not configurable.
type failurePatternVersion struct {
	extractorVersion    string
	resultSchemaVersion int64
}

func compiledFailurePatternVersion() failurePatternVersion {
	return failurePatternVersion{cifailurepatterns.Version, resultSchemaVersion}
}

type input struct {
	observation  cifailures.Observation
	multiplicity int64
	nameStatus   string
	// Prepared inputs are immutable, including their resolved semantic context.
	key                string
	preparedForVersion failurePatternVersion
}

// resultRow is the complete computation, not an aggregate or a display-limited report.
// It carries raw identity but not raw content: message and timings are read from
// ciTestResults by joining on (buildId, source, testId, messageHash): a test can
// fail more than once in a run, and the message hash tells those raw rows apart.
// Finding locations are byte offsets into that raw message.
// Multiplicity belongs exclusively to current raw inputs.
type resultRow struct {
	ResultKey           string                      `json:"resultKey"`
	RawFingerprint      string                      `json:"rawFingerprint"`
	MessageHash         string                      `json:"messageHash"`
	ExtractorVersion    string                      `json:"extractorVersion"`
	ResultSchemaVersion int64                       `json:"resultSchemaVersion"`
	Origin              string                      `json:"origin"`
	BuildID             string                      `json:"buildId"`
	Source              string                      `json:"source"`
	TestID              string                      `json:"testId"`
	TestName            string                      `json:"testName"`
	Findings            []cifailurepatterns.Finding `json:"findings"`
	ComputedAt          time.Time                   `json:"computedAt"`
	ProducerCommit      string                      `json:"producerCommit"`
}

// messageHash is the lowercase hex SHA-256 of the raw message bytes, empty for an
// empty message. It equals Kusto's hash_sha256(message), including that empty
// strings hash to the empty string, so consumers can join results to the exact
// raw row with hash_sha256(message) when a test reports several failures.
func messageHash(message string) string {
	if message == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(message))
	return hex.EncodeToString(sum[:])
}

func resultKey(rawFingerprint, name string, version failurePatternVersion) string {
	h := sha256.New()
	var length [8]byte
	for _, field := range [...]string{"ci-failure-pattern-result-v1", rawFingerprint, name, version.extractorVersion, strconv.FormatInt(version.resultSchemaVersion, 10)} {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func prepareInput(i input, version failurePatternVersion) input {
	if i.key != "" && i.preparedForVersion == version {
		return i
	}
	raw := i.observation.Fingerprint
	if i.key == "" {
		raw = cifailures.Fingerprint(i.observation)
	}
	i.observation.Fingerprint = raw
	i.key, i.preparedForVersion = resultKey(raw, i.observation.TestName, version), version
	return i
}

func (i input) keyFor(version failurePatternVersion) string { return prepareInput(i, version).key }

func envelope(i input, version failurePatternVersion) resultRow {
	i = prepareInput(i, version)
	o := i.observation
	return resultRow{
		ResultKey: i.key, RawFingerprint: o.Fingerprint, MessageHash: messageHash(o.Text),
		ExtractorVersion: version.extractorVersion, ResultSchemaVersion: version.resultSchemaVersion,
		Origin: o.Origin, BuildID: o.BuildID, Source: o.Source, TestID: o.TestID, TestName: o.TestName,
	}
}
