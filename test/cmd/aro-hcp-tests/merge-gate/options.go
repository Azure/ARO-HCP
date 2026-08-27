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

// Package mergegate implements the `merge-gate` subcommand: it asks the
// release-dashboard merge-gate API whether the PR (or batch) described by the prow
// JOB_SPEC should be allowed to merge, and fails the test run if it should not.
package mergegate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/wait"
)

const mergeGatePath = "/api/merge-gate"

// ameliorationDocsURL points at the guide for unblocking a merge by declaring a
// fix with an Ameliorates-Alert commit trailer.
const ameliorationDocsURL = "https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/merge-gate.md"

// Retry backoff parameters for transient (503/network) failures.
const (
	retryBaseDelay = 1 * time.Second
	retryFactor    = 2.0
	retryJitter    = 0.2
	retryCap       = 30 * time.Second
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		JobSpec:     os.Getenv("JOB_SPEC"),
		Token:       os.Getenv("RELEASE_DASHBOARD_TOKEN"),
		URL:         os.Getenv("RELEASE_DASHBOARD_URL"),
		Timeout:     30 * time.Second,
		Retries:     3,
		FailOnError: true,
	}
}

// BindOptions registers the merge-gate flags.
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.URL, "url", opts.URL, "Base URL of the release dashboard (defaults to RELEASE_DASHBOARD_URL).")
	cmd.Flags().StringVar(&opts.JobSpec, "job-spec", opts.JobSpec, "Prow JOB_SPEC JSON (defaults to the JOB_SPEC environment variable).")
	cmd.Flags().StringVar(&opts.JobSpecFile, "job-spec-file", opts.JobSpecFile, "Path to a file containing the prow JOB_SPEC JSON.")
	cmd.Flags().StringVar(&opts.Token, "token", opts.Token, "Optional bearer token for the endpoint (defaults to RELEASE_DASHBOARD_TOKEN).")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout for a single request to the merge gate.")
	cmd.Flags().IntVar(&opts.Retries, "retries", opts.Retries, "Number of additional attempts on transient (503/network) failures.")
	cmd.Flags().BoolVar(&opts.FailOnError, "fail-on-error", opts.FailOnError, "Treat an indeterminate verdict (or an unreachable gate) as a block.")
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	// URL is the base URL of the release dashboard (the /api/merge-gate path is
	// appended automatically).
	URL string
	// JobSpec is the prow JOB_SPEC JSON. Defaults to the JOB_SPEC env var.
	JobSpec string
	// JobSpecFile, if set, is read instead of JobSpec.
	JobSpecFile string
	// Token is an optional bearer token for the endpoint.
	Token string
	// Timeout bounds a single HTTP attempt.
	Timeout time.Duration
	// Retries is the number of additional attempts on transient failures.
	Retries int
	// FailOnError treats an indeterminate (error) verdict as a block.
	FailOnError bool
}

type validatedOptions struct {
	*RawOptions
	endpoint string
	jobSpec  []byte
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	*validatedOptions
	client *http.Client
}

type Options struct {
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.URL == "" {
		return nil, fmt.Errorf("the release dashboard URL must be provided with --url (or RELEASE_DASHBOARD_URL)")
	}
	u, err := url.Parse(o.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid --url %q: %w", o.URL, err)
	}
	// Never send a bearer token in cleartext: require https, except when talking to
	// a loopback address for local testing.
	if o.Token != "" && !isSecureURL(u) {
		return nil, fmt.Errorf("refusing to send a bearer token to insecure URL %q; use https (http is allowed only for localhost)", o.URL)
	}

	jobSpec := []byte(o.JobSpec)
	if o.JobSpecFile != "" {
		data, err := os.ReadFile(o.JobSpecFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --job-spec-file %q: %w", o.JobSpecFile, err)
		}
		jobSpec = data
	}
	if len(bytes.TrimSpace(jobSpec)) == 0 {
		return nil, fmt.Errorf("a prow job spec must be provided via JOB_SPEC, --job-spec, or --job-spec-file")
	}
	// Fail fast on obviously malformed input.
	if !json.Valid(jobSpec) {
		return nil, fmt.Errorf("the provided job spec is not valid JSON")
	}

	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Retries < 0 {
		o.Retries = 0
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
			endpoint:   strings.TrimRight(o.URL, "/") + mergeGatePath,
			jobSpec:    jobSpec,
		},
	}, nil
}

// isSecureURL reports whether u is safe to send a bearer token to: https anywhere,
// or http to a loopback host (for local testing).
func isSecureURL(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return true
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
	}
	return false
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	return &Options{
		completedOptions: &completedOptions{
			validatedOptions: o.validatedOptions,
			client:           &http.Client{Timeout: o.Timeout},
		},
	}, nil
}

// verdict mirrors the release-dashboard response body.
type verdict struct {
	Merge  bool   `json:"merge"`
	Reason string `json:"reason"`
	ADXURL string `json:"adxUrl"`
}

// Run queries the merge-gate API and returns an error (failing the test) when the
// PR should not merge.
func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	v, err := o.query(ctx, logger)
	if err != nil {
		if o.FailOnError {
			return fmt.Errorf("merge gate could not be evaluated (failing closed): %w", err)
		}
		logger.Error(err, "Merge gate could not be evaluated; allowing due to --fail-on-error=false.")
		return nil
	}

	logger.Info("Merge gate verdict.", "merge", v.Merge, "reason", v.Reason, "adxUrl", v.ADXURL)
	if v.ADXURL != "" {
		fmt.Fprintf(os.Stderr, "\nInspect the alerts considered by this decision:\n  %s\n\n", v.ADXURL)
	}

	if v.Merge {
		logger.Info("Merge gate passed.", "reason", v.Reason)
		return nil
	}

	if v.Reason == "error" && !o.FailOnError {
		logger.Error(nil, "Merge gate returned an indeterminate verdict; allowing due to --fail-on-error=false.")
		return nil
	}

	fmt.Fprintf(os.Stderr, `
This merge is blocked by production alerts for the components it touches.
To unblock, do one of:
  1. Limit the PR to bug fixes: make every commit use the conventional "fix:"
     prefix (fix:, fix(scope):, fix!:), which exempts the PR from this gate.
  2. Understand the alerts shown above and declare each one fixed with an
     "Ameliorates-Alert:" trailer in the relevant commit message.
See %s

`, ameliorationDocsURL)
	return fmt.Errorf("merge gate blocked the merge (reason: %s)", v.Reason)
}

// query POSTs the job spec, retrying transient failures (network errors and 503)
// with exponential backoff and jitter.
func (o *Options) query(ctx context.Context, logger logr.Logger) (verdict, error) {
	backoff := wait.Backoff{
		Duration: retryBaseDelay,
		Factor:   retryFactor,
		Jitter:   retryJitter,
		Cap:      retryCap,
		Steps:    o.Retries + 1,
	}

	var (
		v       verdict
		lastErr error
	)
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		got, retryable, attemptErr := o.attempt(ctx)
		if attemptErr == nil {
			v = got
			return true, nil
		}
		if !retryable {
			return false, attemptErr
		}
		lastErr = attemptErr
		logger.Error(attemptErr, "Transient merge-gate failure; will retry.")
		return false, nil
	})
	if err != nil {
		// On retry exhaustion wait returns a timeout error; surface the last
		// transient failure, which is more actionable.
		if lastErr != nil {
			return verdict{}, lastErr
		}
		return verdict{}, err
	}
	return v, nil
}

// attempt performs one HTTP request. The bool reports whether the error is
// retryable (transient).
func (o *Options) attempt(ctx context.Context) (verdict, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(o.jobSpec))
	if err != nil {
		return verdict{}, false, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return verdict{}, true, fmt.Errorf("request to %s failed: %w", o.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return verdict{}, true, fmt.Errorf("failed to read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var v verdict
		if err := json.Unmarshal(body, &v); err != nil {
			return verdict{}, false, fmt.Errorf("failed to parse verdict %q: %w", string(body), err)
		}
		return v, false, nil
	case http.StatusServiceUnavailable:
		return verdict{}, true, fmt.Errorf("merge gate temporarily unavailable (503): %s", strings.TrimSpace(string(body)))
	default:
		return verdict{}, false, fmt.Errorf("unexpected status %d from merge gate: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
