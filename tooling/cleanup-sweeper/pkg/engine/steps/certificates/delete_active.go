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

package certificates

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

// deleteActiveStep soft-deletes policy-selected active certificates and, when
// purging is enabled, immediately purges each resulting tombstone.
type deleteActiveStep struct {
	session *Session
	certs   map[string]*azcertificates.CertificateProperties
}

var _ runner.Step = (*deleteActiveStep)(nil)

func (s *deleteActiveStep) Name() string { return "Delete and purge active certificates" }

func (s *deleteActiveStep) Discover(ctx context.Context) ([]runner.Target, error) {
	candidates, err := s.session.discoverActive(ctx)
	if err != nil {
		return nil, err
	}
	s.certs = make(map[string]*azcertificates.CertificateProperties, len(candidates))
	targets := make([]runner.Target, 0, len(candidates))
	for idx, cert := range candidates {
		name, _, _ := certificateID(s.session.vaultHost, cert.ID)
		target := runner.Target{ID: fmt.Sprintf("delete/%d/%s", idx, name), Name: name, Type: certificateType}
		s.certs[target.ID] = cert
		targets = append(targets, target)
	}
	return targets, nil
}

func (s *deleteActiveStep) Delete(ctx context.Context, target runner.Target, _ bool) error {
	cert, ok := s.certs[target.ID]
	if !ok {
		return fmt.Errorf("unknown certificate cleanup target %q", target.ID)
	}
	counts, err := s.session.deleteActiveTarget(ctx, cert)
	s.session.record("delete", target.Name, counts, err)
	return err
}

func (s *deleteActiveStep) Verify(context.Context) error { return nil }
func (s *deleteActiveStep) RetryLimit() int              { return 1 }
func (s *deleteActiveStep) ContinueOnError() bool        { return true }

func (s *Session) discoverActive(ctx context.Context) ([]*azcertificates.CertificateProperties, error) {
	// The initial inventory only drives selection; deletes revalidate against
	// the guard, which refreshes it. Failure is fatal: no target set can be
	// established safely.
	var owners sets.Set[string]
	if s.policy.RequiresOwnerInventory() {
		var err error
		if owners, _, err = s.guard.owners(ctx); err != nil {
			return nil, err
		}
	}
	logger := s.logger(ctx)
	now := s.now()

	limit := s.maxDeletions
	var candidates []*azcertificates.CertificateProperties
	seen := map[string]bool{}
	pager := s.client.NewListCertificatePropertiesPager(nil)
	for pager.More() {
		if len(candidates) >= limit {
			s.count(func(c *summary) { c.Skipped["limit"]++ })
			break
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list certificate metadata (no changes attempted): %w", err)
		}
		for _, cert := range page.Value {
			if len(candidates) >= limit {
				s.count(func(c *summary) { c.Skipped["limit"]++ })
				return candidates, nil
			}
			s.count(func(c *summary) { c.Scanned++ })
			name, selection, decision := s.eligible(cert, owners, now)
			if !decision.Selected() {
				s.count(func(c *summary) { c.Skipped[string(decision)]++ })
				continue
			}
			// Duplicate names indicate a changing/non-snapshot listing. Fail closed.
			if seen[name] {
				return nil, fmt.Errorf("duplicate certificate %q in inventory", name)
			}
			seen[name] = true
			s.count(func(c *summary) { c.Eligible++; c.Selected++ })
			candidates = append(candidates, cert)
			logger.Info(
				"Selected CI certificate",
				"name", name,
				"owner", selection.OwnerToken,
				"policyRule", selection.RuleName,
				"reason", fmt.Sprintf("rule[%d]-expired", selection.RuleIndex),
				"created", cert.Attributes.Created,
				"updated", cert.Attributes.Updated,
			)
		}
	}
	return candidates, nil
}
