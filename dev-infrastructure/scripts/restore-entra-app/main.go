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
//
// restore-entra-app: restore a soft-deleted Geneva Actions Entra
// application and service principal before the pipeline step that
// creates them runs.
//
// Background
// ----------
// Entra applications that are deleted (whether by automation, manual
// cleanup, or subscription-level teardown) enter a 30-day soft-delete
// window. During that window, a fresh az ad app create / Bicep
// deployment with the same identifierUri or displayName will fail with
// a conflict. Restoring the soft-deleted object first clears the
// conflict so the subsequent create-or-update step succeeds.
//
// This binary is idempotent:
//   - Active app exists        -> exit 0 (no-op)
//   - No active, no deleted    -> exit 0 (subsequent step creates it)
//   - Deleted app found        -> restore app (+ SP if also deleted)
//
// Inputs (env vars)
// -----------------
//   APPLICATION_NAME  Entra app displayName (e.g. arohcp-ga-stg)
//   DRY_RUN           "true" to log intended actions without executing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	graphBaseURL   = "https://graph.microsoft.com/v1.0"
	graphScope     = "https://graph.microsoft.com/.default"
	overallTimeout = 2 * time.Minute
)

func main() {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: false,
	})
	slog.SetDefault(slog.New(handler).With("component", "restore-entra-app"))

	if err := run(); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logBanner("STARTUP")
	cfg.logEnv()

	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return fmt.Errorf("azidentity: %w", err)
	}

	gc := newGraphClient(cred)
	return runWith(ctx, cfg, gc)
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

type config struct {
	applicationName string
	dryRun          bool
}

func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		applicationName: env("APPLICATION_NAME"),
	}
	if c.applicationName == "" {
		return nil, errors.New("APPLICATION_NAME is required")
	}
	if strings.ContainsAny(c.applicationName, "'\"") {
		return nil, fmt.Errorf("APPLICATION_NAME contains quotes: %q", c.applicationName)
	}
	if v := strings.ToLower(strings.TrimSpace(env("DRY_RUN"))); v == "true" || v == "1" || v == "yes" {
		c.dryRun = true
	}
	return c, nil
}

func loadConfig() (*config, error) {
	return parseEnvConfig(os.Getenv)
}

func (c *config) logEnv() {
	logf("APPLICATION_NAME=%s", c.applicationName)
	logf("DRY_RUN=%t", c.dryRun)
}

// ---------------------------------------------------------------------------
// restorer interface (for testability)
// ---------------------------------------------------------------------------

type restorer interface {
	getActiveApp(ctx context.Context, displayName string) (string, error)
	getDeletedApp(ctx context.Context, displayName string) (string, error)
	getDeletedSP(ctx context.Context, displayName string) (string, error)
	restore(ctx context.Context, objectType, objectID string) error
}

// ---------------------------------------------------------------------------
// graphClient implements restorer
// ---------------------------------------------------------------------------

type graphClient struct {
	pl runtime.Pipeline
}

func newGraphClient(cred azcore.TokenCredential) *graphClient {
	return &graphClient{
		pl: runtime.NewPipeline("restore-entra-app", "1.0.0",
			runtime.PipelineOptions{
				PerRetry: []policy.Policy{
					runtime.NewBearerTokenPolicy(cred, []string{graphScope}, nil),
				},
			},
			&policy.ClientOptions{}),
	}
}

type graphListResponse struct {
	Value []struct {
		ID string `json:"id"`
	} `json:"value"`
}

func (g *graphClient) doGetFirstID(ctx context.Context, rawURL string) (string, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := g.pl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return "", runtime.NewResponseError(resp)
	}
	var result graphListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(result.Value) == 0 {
		return "", nil
	}
	return result.Value[0].ID, nil
}

func odataFilterURL(base, displayName string) string {
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("$filter", fmt.Sprintf("displayName eq '%s'", displayName))
	q.Set("$select", "id")
	u.RawQuery = q.Encode()
	return u.String()
}

func (g *graphClient) getActiveApp(ctx context.Context, displayName string) (string, error) {
	return g.doGetFirstID(ctx, odataFilterURL(graphBaseURL+"/applications", displayName))
}

func (g *graphClient) getDeletedApp(ctx context.Context, displayName string) (string, error) {
	return g.doGetFirstID(ctx, odataFilterURL(graphBaseURL+"/directory/deletedItems/microsoft.graph.application", displayName))
}

func (g *graphClient) getDeletedSP(ctx context.Context, displayName string) (string, error) {
	return g.doGetFirstID(ctx, odataFilterURL(graphBaseURL+"/directory/deletedItems/microsoft.graph.servicePrincipal", displayName))
}

func (g *graphClient) restore(ctx context.Context, objectType, objectID string) error {
	endpoint := fmt.Sprintf("%s/directory/deletedItems/%s/restore", graphBaseURL, url.PathEscape(objectID))
	req, err := runtime.NewRequest(ctx, http.MethodPost, endpoint)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := g.pl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return runtime.NewResponseError(resp)
	}
	return nil
}

// ---------------------------------------------------------------------------
// orchestration
// ---------------------------------------------------------------------------

func runWith(ctx context.Context, cfg *config, r restorer) error {
	logBanner("CHECK ACTIVE APPLICATION")
	activeID, err := r.getActiveApp(ctx, cfg.applicationName)
	if err != nil {
		return fmt.Errorf("check active application: %w", err)
	}
	if activeID != "" {
		logf("active application %q (objectId=%s) exists; no restore needed", cfg.applicationName, activeID)
		return nil
	}
	logf("no active application named %q found", cfg.applicationName)

	logBanner("CHECK DELETED APPLICATION")
	deletedAppID, err := r.getDeletedApp(ctx, cfg.applicationName)
	if err != nil {
		return fmt.Errorf("check deleted application: %w", err)
	}
	if deletedAppID == "" {
		logf("no soft-deleted application named %q found; will be created by subsequent step", cfg.applicationName)
		return nil
	}
	logf("found soft-deleted application %q (objectId=%s)", cfg.applicationName, deletedAppID)

	if cfg.dryRun {
		logf("DRY_RUN=true — would restore application %s", deletedAppID)
	} else {
		logf("restoring application %s", deletedAppID)
		if err := r.restore(ctx, "application", deletedAppID); err != nil {
			return fmt.Errorf("restore application: %w", err)
		}
		logf("application restored")
	}

	logBanner("CHECK DELETED SERVICE PRINCIPAL")
	deletedSPID, err := r.getDeletedSP(ctx, cfg.applicationName)
	if err != nil {
		return fmt.Errorf("check deleted service principal: %w", err)
	}
	if deletedSPID == "" {
		logf("no soft-deleted service principal for %q; subsequent Bicep step will recreate via manageSp=true", cfg.applicationName)
	} else if cfg.dryRun {
		logf("DRY_RUN=true — would restore service principal %s", deletedSPID)
	} else {
		logf("restoring service principal %s", deletedSPID)
		if err := r.restore(ctx, "servicePrincipal", deletedSPID); err != nil {
			return fmt.Errorf("restore service principal: %w", err)
		}
		logf("service principal restored")
	}

	logf("restore complete")
	return nil
}

// ---------------------------------------------------------------------------
// logging
// ---------------------------------------------------------------------------

func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch {
	case strings.HasPrefix(msg, "WARN:"):
		slog.Warn(strings.TrimSpace(strings.TrimPrefix(msg, "WARN:")))
	case strings.HasPrefix(msg, "ERROR:"):
		slog.Error(strings.TrimSpace(strings.TrimPrefix(msg, "ERROR:")))
	default:
		slog.Info(msg)
	}
}

func logBanner(s string) {
	slog.Info(strings.Repeat("=", 60), "phase", s)
	slog.Info(">>> "+s, "phase", s)
	slog.Info(strings.Repeat("=", 60), "phase", s)
}
