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

package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// retryAfterTimeout is an overall safety bound on how long a GET will keep
	// retrying, so a server that never stops sending Retry-After can't hang the
	// test. The wait *between* retries is dictated by the server's Retry-After
	// header, not by this value.
	retryAfterTimeout = 60 * time.Second
	// retryAfterFallbackInterval is used only when a Retry-After header is
	// present but its value can't be parsed (or is non-positive), so the loop
	// still makes progress without busy-looping.
	retryAfterFallbackInterval = 250 * time.Millisecond
)

type httpGetStep struct {
	stepID StepID
	key    ResourceKey

	expectedResource map[string]any
	expectedError    string
}

func newHTTPGetStep(stepID StepID, stepDir fs.FS) (*httpGetStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key ResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	var expectedResource map[string]any
	expectedResources, err := readResourcesInDir[map[string]any](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}
	switch len(expectedResources) {
	case 0:
	case 1:
		expectedResource = *expectedResources[0]
	default:
		return nil, fmt.Errorf("cannot expect more than one resource")
	}

	if len(expectedError) == 0 && expectedResource == nil {
		return nil, fmt.Errorf("must expect either error and value")
	}

	return &httpGetStep{
		stepID:           stepID,
		key:              key,
		expectedResource: expectedResource,
		expectedError:    expectedError,
	}, nil
}

var _ IntegrationTestStep = &httpGetStep{}

func (l *httpGetStep) StepID() StepID {
	return l.stepID
}

func (l *httpGetStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	accessor := stepInput.HTTPTestAccessor(l.key)

	// Retry only while the response does not already match the expectation AND
	// the server asked us to try again via Retry-After. A response that matches
	// on the first attempt (every ordinary GET, including one that legitimately
	// asserts a not-ready/Retry-After body) returns immediately; a definitive
	// non-matching response without Retry-After fails without retrying. This
	// lets reads tolerate eventual consistency (e.g. an informer lister cache)
	// exactly as a real client following the Retry-After contract would, with no
	// per-step configuration.
	//
	// Bounding the retries with a context rather than a manual deadline means an
	// over-large Retry-After can't overshoot retryAfterTimeout: whichever of the
	// two fires first wins the select below. The GET itself keeps the parent
	// ctx, so a slow in-flight request isn't turned into a context error.
	retryCtx, cancel := context.WithTimeout(ctx, retryAfterTimeout)
	defer cancel()

	for {
		resp, err := accessor.Get(ctx, l.key.ResourceID)

		// Capture the headers before decoding, since DecodeResponseBody
		// consumes and closes the body.
		var header http.Header
		var body any
		if err == nil {
			header = resp.Header
			body, err = DecodeResponseBody(resp)
		}

		if l.matches(t, body, err) {
			return
		}

		delay, retryRequested := retryAfterDelay(header)
		if !retryRequested {
			// Definitive response: assert once so the failure output is the
			// same as a plain single GET.
			l.assert(t, body, err)
			return
		}

		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			// Out of budget (or the parent ctx went away). Assert on the last
			// response rather than reporting a context error, so the failure
			// shows the actual/expected diff that kept us retrying.
			l.assert(t, body, err)
			return
		case <-timer.C:
		}
	}
}

// matches reports whether the response satisfies the step's expectation without
// failing the test, so it is safe to call repeatedly in the retry loop.
func (l *httpGetStep) matches(t *testing.T, body any, err error) bool {
	if len(l.expectedError) > 0 {
		return err != nil && strings.Contains(err.Error(), l.expectedError)
	}
	if err != nil {
		return false
	}
	_, equals := ResourceInstanceEquals(t, l.expectedResource, body)
	return equals
}

// assert performs the terminal comparison, failing the test on mismatch.
func (l *httpGetStep) assert(t *testing.T, body any, err error) {
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

	if diff, equals := ResourceInstanceEquals(t, l.expectedResource, body); !equals {
		t.Logf("actual:\n%v", stringifyResource(body))
		t.Error(diff)
	}
}

// retryAfterDelay parses the Retry-After header. It returns whether a retry was
// requested (i.e. the header is present) and, if so, how long to wait before the
// next attempt as dictated by the server. Both forms from RFC 7231 are
// supported: delta-seconds ("5") and an HTTP-date. A present-but-unparseable or
// non-positive value falls back to retryAfterFallbackInterval so the loop still
// makes progress.
func retryAfterDelay(header http.Header) (time.Duration, bool) {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second, true
		}
		return retryAfterFallbackInterval, true
	}

	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay, true
		}
		return retryAfterFallbackInterval, true
	}

	return retryAfterFallbackInterval, true
}
