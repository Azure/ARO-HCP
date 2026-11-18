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
// The tool is one-shot: it is deleted together with its pipeline step once live
// confirmation shows the certificates are gone.
//
// The tool is best effort by design. It always exits 0 so it can run as an
// unattended pipeline step: every failure is reported in the run summary and in
// the evidence file instead of failing the step.
//
// Inputs (environment variables, set by the pipeline step):
//
//	CERT_VAULTS           - comma separated vault names, expanded to https://<name>.vault.azure.net
//	CERT_PATTERNS         - comma separated glob patterns matched against certificate names
//	CLEANUP_MODE          - "inspect" (default) or "apply"
//	CLEANUP_DRY_RUN       - "true" (default) reports what apply would delete without deleting
//	CLEANUP_MAX_DELETIONS - upper bound per vault, so an over-broad pattern cannot sweep a vault
//	EVIDENCE_PATH         - optional file to write a JSON line per certificate
//	LOG_VERBOSITY         - optional slog verbosity (default 0)
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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

// defaultMaxDeletions bounds one run per vault. The patterns are the only
// selector, so a pattern that widens by accident must stop the run instead of
// deleting every certificate it happens to match.
const defaultMaxDeletions = 10

// certificate is the subset of the Key Vault certificate item this tool needs.
type certificate struct {
	ID        string
	Name      string
	Version   string
	Expires   time.Time
	HasExpiry bool
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
	NoMatch   int
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
		if _, err := fmt.Sscanf(v, "%d", &verbosity); err != nil {
			verbosity = 0
		}
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
	maxDeletions := defaultMaxDeletions
	if v := strings.TrimSpace(getenv("CLEANUP_MAX_DELETIONS")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			logger.Warn("ignoring invalid CLEANUP_MAX_DELETIONS", "value", v)
		} else {
			maxDeletions = parsed
		}
	}

	evidence, closeEvidence, err := openEvidence(getenv("EVIDENCE_PATH"))
	if err != nil {
		logger.Warn("cannot open evidence file, continuing without it", "err", err)
	}
	if closeEvidence != nil {
		defer closeEvidence()
	}

	cred, credErr := azidentity.NewDefaultAzureCredential(nil)
	if credErr != nil {
		logger.Warn("no Azure credential available, reporting only", "err", credErr)
	}

	var sum summary
	for _, vault := range vaults {
		baseURL := vaultBaseURL(vault)
		if credErr != nil {
			sum.Failed++
			emit(evidence, logger, record{Vault: vault, Action: "list", Result: "failed", Detail: "no credential"})
			continue
		}
		client, err := azcertificates.NewClient(baseURL, cred, nil)
		if err != nil {
			sum.Failed++
			logger.Warn("cannot create certificate client", "vault", vault, "err", err)
			emit(evidence, logger, record{Vault: vault, Action: "list", Result: "failed", Detail: err.Error()})
			continue
		}
		certs, err := listCertificates(ctx, client)
		if err != nil {
			sum.Failed++
			logger.Warn("cannot list certificates", "vault", vault, "err", err)
			emit(evidence, logger, record{Vault: vault, Action: "list", Result: "failed", Detail: err.Error()})
			continue
		}
		matches := filterCertificates(certs, patterns)
		sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
		if len(matches) == 0 {
			sum.NoMatch++
			logger.Warn("pattern matched no certificate in vault", "vault", vault, "patterns", patterns, "listed", len(certs))
		}
		if len(matches) > maxDeletions {
			sum.Skipped += len(matches)
			logger.Warn("more matches than CLEANUP_MAX_DELETIONS, skipping vault", "vault", vault, "matches", len(matches), "max", maxDeletions)
			for _, cert := range matches {
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "skipped", Detail: "exceeds CLEANUP_MAX_DELETIONS"})
			}
			continue
		}
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
			err := deleteCertificate(ctx, client, cert.Name)
			switch {
			case err == nil:
				sum.Deleted++
				logger.Info("deleted", "vault", vault, "name", cert.Name, "version", cert.Version, "recoverable", "see deleted certificates")
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "deleted"})
			case isAlreadyDeleted(err):
				sum.Skipped++
				logger.Info("already deleted", "vault", vault, "name", cert.Name)
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "already-deleted", Detail: err.Error()})
			default:
				sum.Failed++
				logger.Warn("delete failed", "vault", vault, "name", cert.Name, "err", err)
				emit(evidence, logger, record{Vault: vault, Certificate: cert.Name, Version: cert.Version, Action: "delete", Result: "failed", Detail: err.Error()})
			}
		}
	}

	if sum.Inspected == 0 && sum.Skipped == 0 {
		logger.Warn("no certificate matched any pattern in any vault")
	}
	logger.Info("summary",
		"mode", mode, "dryRun", dryRun, "maxDeletions", maxDeletions,
		"inspected", sum.Inspected, "planned", sum.Planned,
		"deleted", sum.Deleted, "skipped", sum.Skipped, "failed", sum.Failed,
		"vaultsWithNoMatch", sum.NoMatch)
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

// splitCertificateID extracts the name and the optional version from a
// certificate id. The list API returns "/certificates/<name>" while a specific
// version reads "/certificates/<name>/<version>", so both shapes are required.
func splitCertificateID(id string) (name, version string, ok bool) {
	parsed, err := url.Parse(id)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 && len(parts) != 3 {
		return "", "", false
	}
	if parts[0] != "certificates" || parts[1] == "" {
		return "", "", false
	}
	if len(parts) == 3 {
		return parts[1], parts[2], true
	}
	return parts[1], "", true
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

// listCertificates reads every page of the certificate list for one vault.
func listCertificates(ctx context.Context, client *azcertificates.Client) ([]certificate, error) {
	var certs []certificate
	pager := client.NewListCertificatePropertiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Value {
			if item == nil || item.ID == nil {
				continue
			}
			name, version, ok := splitCertificateID(string(*item.ID))
			if !ok {
				continue
			}
			cert := certificate{ID: string(*item.ID), Name: name, Version: version}
			if item.Attributes != nil && item.Attributes.Expires != nil {
				cert.Expires = item.Attributes.Expires.UTC()
				cert.HasExpiry = true
			}
			certs = append(certs, cert)
		}
	}
	return certs, nil
}

// deleteCertificate soft-deletes one certificate. Key Vault keeps it recoverable.
func deleteCertificate(ctx context.Context, client *azcertificates.Client, name string) error {
	_, err := client.DeleteCertificate(ctx, name, nil)
	return err
}

// isAlreadyDeleted reports whether a delete failed only because the name is
// already a tombstone, which is the state this tool exists to reach.
func isAlreadyDeleted(err error) bool {
	var responseErr *azcore.ResponseError
	if !errors.As(err, &responseErr) {
		return false
	}
	if responseErr.StatusCode == http.StatusNotFound {
		return true
	}
	return responseErr.StatusCode == http.StatusConflict
}
