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

package monitor

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// DefaultResultsBucket is the GCS bucket Prow copies finished-run artifacts
// to; the monitor reads each failed run's ci-operator step graph from it.
const DefaultResultsBucket = "test-platform-results-public"

// maxReasonRunes bounds the failure_reason label value so a pathological
// error message cannot bloat every scrape.
const maxReasonRunes = 200

// ansiRegexp matches ANSI SGR color sequences with or without the ESC
// prefix: the junit served over the GCS API carries the CSI payloads bare
// (raw ESC does not survive the XML encoding).
var ansiRegexp = regexp.MustCompile(`\x1b?\[[0-9;]*m`)

// ReasonClient classifies why a Prow run failed. Reason is empty when no
// classification is available ("where available" per the alerting ticket).
type ReasonClient interface {
	FailureReason(ctx context.Context, jobName, buildID string) string
}

// GCSReasonClient reads a run's junit step graph from the Prow results bucket
// over the public GCS JSON API and reduces it to one line naming the failing
// step and its first error message.
type GCSReasonClient struct {
	baseURL string
	bucket  string
	http    *http.Client
}

// NewGCSReasonClient returns a ReasonClient reading junit step graphs from
// the given bucket.
func NewGCSReasonClient(bucket string, timeout time.Duration) *GCSReasonClient {
	return &GCSReasonClient{
		baseURL: "https://storage.googleapis.com",
		bucket:  bucket,
		http:    &http.Client{Timeout: timeout},
	}
}

// FailureReason fetches and classifies the run's step graph. Failures to
// fetch or parse degrade to an empty reason: a missing classification must
// never fail the whole poll.
func (c *GCSReasonClient) FailureReason(ctx context.Context, jobName, buildID string) string {
	object := fmt.Sprintf("logs/%s/%s/artifacts/junit_operator.xml", jobName, buildID)
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media", c.baseURL, c.bucket, url.PathEscape(object))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	reason, err := classifyJUnit(data)
	if err != nil {
		return ""
	}
	return reason
}

type junitSuites struct {
	Suites []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	TestCases []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name    string      `xml:"name,attr"`
	Failure *junitBody  `xml:"failure"`
	Error   *junitBody  `xml:"error"`
}

type junitBody struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// classifyJUnit reduces a ci-operator step-graph junit to "step name: first
// error line". Unparseable XML returns an error; a graph with no failing
// step returns an empty string.
func classifyJUnit(data []byte) (string, error) {
	var suites junitSuites
	if err := xml.Unmarshal(data, &suites); err != nil {
		return "", fmt.Errorf("parse junit: %w", err)
	}

	for _, suite := range suites.Suites {
		for _, tc := range suite.TestCases {
			body := tc.Failure
			if body == nil {
				body = tc.Error
			}
			if body == nil {
				continue
			}
			line := firstErrorLine(body.Message + "\n" + body.Text)
			if line == "" {
				continue
			}
			if tc.Name == "" {
				return truncateRunes(line, maxReasonRunes), nil
			}
			return truncateRunes(tc.Name+": "+line, maxReasonRunes), nil
		}
	}
	return "", nil
}

// logTimestampRegexp matches the bracketed log timestamps the tooling's
// logger prefixes lines with (e.g. "[14:20:38.819]").
var logTimestampRegexp = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}(?:\.\d+)?\]`)

// cleanReasonText strips ANSI escapes plus the non-ASCII and control bytes
// ci-operator's log capture leaves behind (0x81, U+FFFD) and normalizes
// whitespace.
func cleanReasonText(text string) string {
	text = ansiRegexp.ReplaceAllString(text, "")
	var b strings.Builder
	for _, r := range text {
		switch {
		case r < 32 && r != '\n', r == 127, r > unicode.MaxASCII:
			continue
		case r == '\t' || r == '\r':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstErrorLine returns the first line that looks like an error, falling
// back to the first non-empty line. A line opening a JSON object continues
// onto the following lines, so the next non-empty line is joined in.
func firstErrorLine(text string) string {
	lines := nonEmptyLines(cleanReasonText(text))
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), "error") {
			continue
		}
		if strings.HasSuffix(line, "{") && i+1 < len(lines) {
			line += lines[i+1]
		}
		return line
	}
	if len(lines) > 0 {
		return lines[0]
	}
	return ""
}

// nonEmptyLines trims each line (leading control junk, quotes, log
// timestamps) and drops empty results.
func nonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimFunc(line, func(r rune) bool {
			return !unicode.IsPrint(r) || r == '"'
		}))
		line = strings.TrimSpace(logTimestampRegexp.ReplaceAllString(line, ""))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func truncateRunes(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

// compile-time interface check.
var _ ReasonClient = (*GCSReasonClient)(nil)
