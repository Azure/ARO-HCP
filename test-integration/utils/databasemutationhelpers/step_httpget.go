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
	deadline := time.Now().Add(retryAfterTimeout)
	for {
		raw, err := accessor.Get(ctx, l.key.ResourceID)

		// On success the accessor must return a *GetResponse; fail fast if the
		// contract is violated rather than silently treating it as an empty
		// response (nil body/headers), which would produce confusing diffs.
		var resp *GetResponse
		if err == nil {
			var ok bool
			resp, ok = raw.(*GetResponse)
			require.Truef(t, ok, "accessor.Get for %s returned %T, want *GetResponse", l.key.ResourceID, raw)
		}

		if l.matches(t, resp, err) {
			return
		}

		var header http.Header
		if resp != nil {
			header = resp.Header
		}
		delay, retryRequested := retryAfterDelay(header)
		if remaining := time.Until(deadline); retryRequested && remaining > 0 {
			// Cap the wait at the remaining time so retryAfterTimeout is a real
			// bound even when the server returns a large Retry-After value.
			if delay > remaining {
				delay = remaining
			}
			select {
			case <-ctx.Done():
				t.Fatalf("context cancelled while retrying GET after Retry-After for %s: %v", l.key.ResourceID, ctx.Err())
				return
			case <-time.After(delay):
				continue
			}
		}

		// Definitive response (no Retry-After) or timed out: assert once so the
		// failure output is the same as a plain single GET.
		l.assert(t, resp, err)
		return
	}
}

// matches reports whether the response satisfies the step's expectation without
// failing the test, so it is safe to call repeatedly in the retry loop.
func (l *httpGetStep) matches(t *testing.T, resp *GetResponse, err error) bool {
	if len(l.expectedError) > 0 {
		return err != nil && strings.Contains(err.Error(), l.expectedError)
	}
	if err != nil {
		return false
	}
	_, equals := ResourceInstanceEquals(t, l.expectedResource, respBody(resp))
	return equals
}

// assert performs the terminal comparison, failing the test on mismatch.
func (l *httpGetStep) assert(t *testing.T, resp *GetResponse, err error) {
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}

	body := respBody(resp)
	if diff, equals := ResourceInstanceEquals(t, l.expectedResource, body); !equals {
		t.Logf("actual:\n%v", stringifyResource(body))
		t.Error(diff)
	}
}

func respBody(resp *GetResponse) any {
	if resp == nil {
		return nil
	}
	return resp.Body
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
