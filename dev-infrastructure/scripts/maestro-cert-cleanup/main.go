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

// maestro-cert-cleanup deletes orphaned Maestro certificates from the Key Vaults
// of a deployed environment.
//
// Maestro is retired: no workload, application or SecretProviderClass consumes
// these certificates, but Key Vault keeps renewing them through AutoRenew. This
// tool is the only way to stop that, because a deployment stack manages ARM
// resources and cannot delete a certificate, which is a data-plane object.
//
// The tool is best effort by design. It always exits 0 so it can run as an
// unattended pipeline step: every failure is reported in the run summary and in
// the evidence file instead of failing the step.
//
// Inputs (environment variables, set by the pipeline step):
//
//	CERT_VAULTS     - comma separated vault names, each expanded to https://<name>.vault.azure.net
//	CERT_PATTERNS   - comma separated glob patterns matched against certificate names
//	CLEANUP_MODE    - "inspect" (default) or "apply"
//	CLEANUP_DRY_RUN - "true" (default) reports what apply would delete without deleting
//	EVIDENCE_PATH   - optional file to write a JSON line per certificate
//	LOG_VERBOSITY   - optional slog verbosity (default 0)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const keyVaultScope = "https://vault.azure.net/.default"

// certificate is the subset of the Key Vault certificate item this tool needs.
type certificate struct {
	ID         string
	Name       string
	Version    string
	Expires    time.Time
	HasExpiry  bool
	Attributes map[string]any
}

// record is one line of evidence, written whether or not the action succeeded.
type record struct {
	Timestamp   string `json:"timestamp"`
	Vault       string `json:"vault"`
	Certificate string `json:"certificate"`
	Version     string `json:"version,omitempty"`
	Action      string `json:"action"`
	Result      string `json:"result"`
	Detail      string `json:"detail,omitempty"`
}

// summary aggregates the run so the caller can report counts without inspecting logs.
type summary struct {
	Inspected int
	Planned   int
	Deleted   int
	Skipped   int
	Failed    int
}

func main() {
	if err := run(context.Background(), os.Getenv, os.Stdout); err != nil {
		// Best effort: report and stay green.
		fmt.Fprintf(os.Stdout, "WARN maestro-cert-cleanup did not complete: %v\n", err)
	}
	os.Exit(0)
}

// run performs the whole operation. It never returns an error that should fail a
// pipeline: the error is reported by the caller and the exit status stays zero.
func run(ctx context.Context, getenv func(string) string, out *os.File) error {
	verbosity := 0
	if v := getenv("LOG_VERBOSITY"); v != "" {
		fmt.Sscanf(v, "%d", &verbosity)
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.Level(-verbosity)}))

	vaults := splitList(getenv("CERT_VAULTS"))
	patterns := splitList(getenv("CERT_PATTERNS"))
	mode := strings.ToLower(strings.TrimSpace(getenv("CLEANUP_MODE")))
	if mode == "" {
		mode = "inspect"
	}
	dryRun := true
	if v := strings.ToLower(strings.TrimSpace(getenv("CLEANUP_DRY_RUN"))); v == "false" {
		dryRun = false
	}

	if len(vaults) == 0 || len(patterns) == 0 {
		logger.Warn("nothing to do: CERT_VAULTS and CERT_PATTERNS must both be set")
		return nil
	}
	if mode != "inspect" && mode != "apply" {
		logger.Warn("unknown CLEANUP_MODE, treating as inspect", "mode", mode)
		mode = "inspect"
	}

	evidence, closeEvidence, err := openEvidence(getenv("EVIDENCE_PATH"))
	if err != nil {
		logger.Warn("cannot open evidence file, continuing without it", "err", err)
	}
	if closeEvidence != nil {
		defer closeEvidence()
	}

	var token string
	cred, credErr := azidentity.NewDefaultAzureCredential(nil)
	if credErr != nil {
		logger.Warn("no Azure credential available, reporting only", "err", credErr)
	} else {
		t, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{keyVaultScope}})
		if err != nil {
			logger.Warn("cannot acquire a Key Vault token, reporting only", "err", err)
		} else {
			token = t.Token
		}
	}

	var sum summary
	for _, vault := range vaults {
		baseURL := vaultBaseURL(vault)
		certs, err := listCertificates(ctx, baseURL, token)
		if err != nil {
			sum.Failed++
			logger.Warn("cannot list certificates", "vault", vault, "err", err)
			emit(evidence, logger, record{Vault: vault, Action: "list", Result: "failed", Detail: err.Error()})
			continue
		}
		matches := filterCertificates(certs, patterns)
		sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
		for _, cert := range matches {
			sum.Inspected++
			logger.Info("certificate", "vault", vault, "name", cert.Name, "version", cert.Version, "expires", cert.Expires.Format(time.RFC3339))
			if mode == "inspect" {
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "inspect", Result: "found"})
				continue
			}
			if dryRun {
				sum.Planned++
				logger.Info("would delete", "vault", vault, "name", cert.Name, "version", cert.Version)
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "planned"})
				continue
			}
			if err := deleteCertificate(ctx, baseURL, cert.Name, token); err != nil {
				sum.Failed++
				logger.Warn("delete failed", "vault", vault, "name", cert.Name, "err", err)
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "failed", Detail: err.Error()})
				continue
			}
			sum.Deleted++
			logger.Info("deleted", "vault", vault, "name", cert.Name, "version", cert.Version, "recoverable", "see deleted certificates")
			emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "deleted"})
		}
	}

	logger.Info("summary",
		"mode", mode, "dryRun", dryRun,
		"inspected", sum.Inspected, "planned", sum.Planned,
		"deleted", sum.Deleted, "skipped", sum.Skipped, "failed", sum.Failed)
	return nil
}

// splitList parses a comma separated environment value, dropping blanks.
func splitList(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// vaultBaseURL expands a vault name into its data-plane URL.
func vaultBaseURL(vault string) string {
	if strings.HasPrefix(vault, "https://") {
		return strings.TrimSuffix(vault, "/")
	}
	return "https://" + vault + ".vault.azure.net"
}

// filterCertificates keeps the certificates whose name matches any pattern.
func filterCertificates(certs []certificate, patterns []string) []certificate {
	var matches []certificate
	for _, cert := range certs {
		for _, pattern := range patterns {
			if ok, _ := filepath.Match(pattern, cert.Name); ok {
				matches = append(matches, cert)
				break
			}
		}
	}
	return matches
}

// openEvidence opens the evidence file when a path is configured.
func openEvidence(path string) (io *os.File, closeFn func(), err error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return file, func() { _ = file.Close() }, nil
}

// emit writes one evidence line; a write failure is logged, never fatal.
func emit(file *os.File, logger *slog.Logger, rec record) {
	if file == nil {
		return
	}
	rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	payload, err := json.Marshal(rec)
	if err != nil {
		logger.Warn("cannot encode evidence record", "err", err)
		return
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		logger.Warn("cannot write evidence record", "err", err)
	}
}

// listCertificates reads the certificate list for one vault.
func listCertificates(ctx context.Context, baseURL, token string) ([]certificate, error) {
	body, err := do(ctx, http.MethodGet, baseURL+"/certificates?api-version=2025-07-01", token)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Value []struct {
			ID         string         `json:"id"`
			Attributes map[string]any `json:"attributes"`
		} `json:"value"`
		NextLink string `json:"nextLink"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode certificate list: %w", err)
	}
	certs := make([]certificate, 0, len(payload.Value))
	for _, item := range payload.Value {
		parts := strings.Split(item.ID, "/")
		if len(parts) < 2 {
			continue
		}
		name, version := parts[len(parts)-2], parts[len(parts)-1]
		cert := certificate{ID: item.ID, Name: name, Version: version, Attributes: item.Attributes}
		if raw, ok := item.Attributes["exp"].(float64); ok {
			cert.Expires = time.Unix(int64(raw), 0).UTC()
			cert.HasExpiry = true
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

// deleteCertificate deletes one certificate. Key Vault keeps it recoverable.
func deleteCertificate(ctx context.Context, baseURL, name, token string) error {
	_, err := do(ctx, http.MethodDelete, baseURL+"/certificates/"+name+"?api-version=2025-07-01", token)
	return err
}

// do performs one Key Vault data-plane request.
func do(ctx context.Context, method, url, token string) ([]byte, error) {
	if token == "" {
		return nil, fmt.Errorf("no Key Vault token")
	}
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
