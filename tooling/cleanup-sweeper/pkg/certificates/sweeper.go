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

// Package certificates implements a deliberately fixed-scope CI cleanup backstop.
package certificates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

const (
	VaultURL                            = "https://aro-hcp-dev-svc-kv.vault.azure.net"
	devInfrastructureSubscription       = "1d3378d3-5a3f-4712-85a1-2485495dfc4b"
	devSharedInfrastructureSubscription = "0ef1ad54-9296-44cd-9600-5dc8e9a74034"
	ownerMaxAge                         = 30 * time.Second
)

var certificateName = regexp.MustCompile(`^(?:(?:frontend-cert|admin-api-cert|sessiongate-cert)-(?:prow|ci00|ci01)|maestro-server)-j([0-9]{7})$`)
var jobToken = regexp.MustCompile(`^j[0-9]{7}$`)

type Options struct {
	DryRun       bool
	MinAge       time.Duration
	MaxDeletions int
}

func (o Options) Validate() error {
	if o.MinAge < 24*time.Hour {
		return fmt.Errorf("--min-age must be at least 24h")
	}
	if o.MaxDeletions <= 0 {
		return fmt.Errorf("--max-deletions must be positive")
	}
	return nil
}

type certificateClient interface {
	NewListCertificatePropertiesPager(*azcertificates.ListCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListCertificatePropertiesResponse]
	GetCertificate(context.Context, string, string, *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
	DeleteCertificate(context.Context, string, *azcertificates.DeleteCertificateOptions) (azcertificates.DeleteCertificateResponse, error)
}

type resourceGroupClient interface {
	NewListPager(*armresources.ResourceGroupsClientListOptions) *runtime.Pager[armresources.ResourceGroupsClientListResponse]
}

type sweeper struct {
	certificates certificateClient
	groups       map[string]resourceGroupClient
	now          func() time.Time
}

// Run always targets the shared dev vault and BOTH dev infrastructure subscriptions.
// The credential needs certificate list/get/delete and subscription-wide RG read permissions.
func Run(ctx context.Context, credential azcore.TokenCredential, opts Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	// Do not replay a DELETE after a retry delay without rerunning the safety checks.
	client, err := azcertificates.NewClient(VaultURL, credential, &azcertificates.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		return err
	}
	s := sweeper{certificates: client, groups: map[string]resourceGroupClient{}, now: time.Now}
	for _, subscription := range []string{devInfrastructureSubscription, devSharedInfrastructureSubscription} {
		client, err := armresources.NewResourceGroupsClient(subscription, credential, nil)
		if err != nil {
			return err
		}
		s.groups[subscription] = client
	}
	return s.run(ctx, opts)
}

type summary struct {
	Scanned       int
	Eligible      int
	Selected      int
	Attempts      int
	Deleted       int
	AlreadyAbsent int
	Failed        int
	Skipped       map[string]int
}

func (s *sweeper) run(ctx context.Context, opts Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	logger := logr.FromContextOrDiscard(ctx).WithValues("vault", VaultURL, "dryRun", opts.DryRun)
	counts := summary{Skipped: map[string]int{}}
	defer func() { logger.Info("CI certificate sweep summary", "counts", counts) }()
	now := s.now()
	cutoff := now.Add(-opts.MinAge)
	var candidates []*azcertificates.CertificateProperties
	seen := map[string]bool{}
	pager := s.certificates.NewListCertificatePropertiesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list certificate metadata (no deletes attempted): %w", err)
		}
		for _, cert := range page.Value {
			counts.Scanned++
			name, _, reason := eligible(cert, now, cutoff)
			if reason != "" {
				counts.Skipped[reason]++
				continue
			}
			// Duplicate names indicate a changing/non-snapshot listing. Fail closed.
			if seen[name] {
				return fmt.Errorf("duplicate certificate %q in inventory", name)
			}
			seen[name] = true
			candidates = append(candidates, cert)
		}
	}
	owners, _, err := s.owners(ctx)
	if err != nil {
		return err
	}
	selected := candidates[:0]
	for _, cert := range candidates {
		name, _, _ := certificateID(cert.ID)
		job := "j" + certificateName.FindStringSubmatch(name)[1]
		if owners[job] {
			counts.Skipped["live-owner"]++
			continue
		}
		counts.Eligible++
		if len(selected) >= opts.MaxDeletions {
			counts.Skipped["limit"]++
			continue
		}
		selected = append(selected, cert)
		counts.Selected++
		logger.Info("Selected CI certificate", "name", name, "job", job, "created", cert.Attributes.Created, "updated", cert.Attributes.Updated)
	}
	if opts.DryRun {
		return ctx.Err()
	}
	var failures []error
	var ownersAt time.Time // Force a fresh complete inventory before the first delete.
	for _, cert := range selected {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if ownersAt.IsZero() || s.now().Sub(ownersAt) >= ownerMaxAge {
			owners, ownersAt, err = s.owners(ctx)
			if err != nil {
				return errors.Join(append(failures, err)...)
			}
		}
		name, version, _ := certificateID(cert.ID)
		job := "j" + certificateName.FindStringSubmatch(name)[1]
		if owners[job] {
			counts.Skipped["owner-revalidation"]++
			continue
		}
		latest, err := s.certificates.GetCertificate(ctx, name, "", nil)
		if isNotFound(err) {
			counts.AlreadyAbsent++
			continue
		}
		if err != nil {
			counts.Failed++
			failures = append(failures, fmt.Errorf("revalidate %s: %w", name, err))
			logger.Error(err, "Certificate revalidation failed", "name", name)
			continue
		}
		current := &azcertificates.CertificateProperties{ID: latest.ID, Attributes: latest.Attributes, Tags: latest.Tags, X509Thumbprint: latest.X509Thumbprint}
		latestName, _, reason := eligible(current, s.now(), cutoff)
		_, latestVersion, _ := certificateID(latest.ID)
		// List IDs are commonly unversioned. In that case exact latest timestamps
		// (and the thumbprint when present) are the snapshot identity, not version history.
		if reason != "" || latestName != name || latestVersion == "" || (version != "" && version != latestVersion) ||
			!cert.Attributes.Created.Equal(*latest.Attributes.Created) || !cert.Attributes.Updated.Equal(*latest.Attributes.Updated) ||
			(len(cert.X509Thumbprint) > 0 && !bytes.Equal(cert.X509Thumbprint, latest.X509Thumbprint)) {
			counts.Skipped["certificate-revalidation"]++
			continue
		}
		// Defer this certificate rather than deleting with a stale owner guard.
		// The next iteration refreshes the inventory, and the next run retries it.
		if s.now().Sub(ownersAt) >= ownerMaxAge {
			counts.Skipped["owner-inventory-expired"]++
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		counts.Attempts++
		_, err = s.certificates.DeleteCertificate(ctx, name, nil)
		switch {
		case isNotFound(err):
			counts.AlreadyAbsent++
		case err != nil:
			counts.Failed++
			failures = append(failures, fmt.Errorf("delete %s: %w", name, err))
			logger.Error(err, "Certificate soft-delete failed", "name", name)
		default:
			counts.Deleted++
		}
	}
	return errors.Join(failures...)
}

func (s *sweeper) owners(ctx context.Context) (map[string]bool, time.Time, error) {
	started := s.now()
	owners := map[string]bool{}
	for _, subscription := range []string{devInfrastructureSubscription, devSharedInfrastructureSubscription} {
		client := s.groups[subscription]
		if client == nil {
			return nil, started, fmt.Errorf("missing required infrastructure subscription %s", subscription)
		}
		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, started, fmt.Errorf("list owner resource groups in %s: %w", subscription, err)
			}
			for _, group := range page.Value {
				if group == nil || group.Name == nil || *group.Name == "" {
					return nil, started, fmt.Errorf("resource group missing name in %s", subscription)
				}
				// Any token in any RG name vetoes deletion, including deleting RGs
				// and collisions with jobs using a different naming convention.
				for _, token := range strings.FieldsFunc(strings.ToLower(*group.Name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
					if jobToken.MatchString(token) {
						owners[token] = true
					}
				}
			}
		}
	}
	if s.now().Sub(started) >= ownerMaxAge {
		return nil, started, fmt.Errorf("owner inventory took at least %s; refusing stale guard", ownerMaxAge)
	}
	return owners, started, nil
}

func eligible(cert *azcertificates.CertificateProperties, now, cutoff time.Time) (string, string, string) {
	if cert == nil {
		return "", "", "invalid-id"
	}
	name, _, valid := certificateID(cert.ID)
	if !valid {
		return "", "", "invalid-id"
	}
	match := certificateName.FindStringSubmatch(name)
	if match == nil {
		return name, "", "name"
	}
	job := "j" + match[1]
	if match[1] <= "0000099" || match[1] == "7654321" {
		return name, job, "placeholder"
	}
	for key, value := range cert.Tags {
		if (strings.EqualFold(key, "persist") || strings.EqualFold(key, "doNotDelete")) && value != nil && strings.EqualFold(strings.TrimSpace(*value), "true") {
			return name, job, "protected-tag"
		}
	}
	a := cert.Attributes
	if a == nil || a.Created == nil || a.Updated == nil || a.Created.IsZero() || a.Updated.IsZero() {
		return name, job, "missing-timestamp"
	}
	if a.Created.After(now) || a.Updated.After(now) {
		return name, job, "future-timestamp"
	}
	if !a.Created.Before(cutoff) || !a.Updated.Before(cutoff) {
		return name, job, "recent"
	}
	return name, job, ""
}

func certificateID(id *azcertificates.ID) (name, version string, valid bool) {
	if id == nil {
		return "", "", false
	}
	u, err := url.Parse(string(*id))
	if err != nil || u.Scheme != "https" || u.Host != strings.TrimPrefix(VaultURL, "https://") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", "", false
	}
	parts := strings.Split(u.Path, "/")
	if (len(parts) != 3 && len(parts) != 4) || parts[0] != "" || parts[1] != "certificates" || parts[2] == "" {
		return "", "", false
	}
	if len(parts) == 4 {
		version = parts[3]
		if version == "" {
			return "", "", false
		}
	}
	return parts[2], version, true
}

func isNotFound(err error) bool {
	var response *azcore.ResponseError
	return errors.As(err, &response) && response.StatusCode == http.StatusNotFound
}
