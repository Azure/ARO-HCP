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

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

// purgeDeletedStep permanently purges policy-selected soft-deleted certificates.
type purgeDeletedStep struct {
	session *Session
	certs   map[string]*azcertificates.DeletedCertificateProperties
}

var _ runner.Step = (*purgeDeletedStep)(nil)

func (s *purgeDeletedStep) Name() string { return "Purge deleted certificates" }

func (s *purgeDeletedStep) Discover(ctx context.Context) ([]runner.Target, error) {
	candidates, err := s.session.discoverDeleted(ctx)
	if err != nil {
		return nil, err
	}
	s.certs = make(map[string]*azcertificates.DeletedCertificateProperties, len(candidates))
	targets := make([]runner.Target, 0, len(candidates))
	for idx, cert := range candidates {
		name, _, _ := certificateID(s.session.vaultHost, cert.ID)
		target := runner.Target{ID: fmt.Sprintf("purge/%d/%s", idx, name), Name: name, Type: certificateType}
		s.certs[target.ID] = cert
		targets = append(targets, target)
	}
	return targets, nil
}

func (s *purgeDeletedStep) Delete(ctx context.Context, target runner.Target, _ bool) error {
	cert, ok := s.certs[target.ID]
	if !ok {
		return fmt.Errorf("unknown certificate cleanup target %q", target.ID)
	}
	counts, err := s.session.purgeDeletedTarget(ctx, cert)
	s.session.record("purge", target.Name, counts, err)
	return err
}

func (s *purgeDeletedStep) Verify(context.Context) error { return nil }
func (s *purgeDeletedStep) RetryLimit() int              { return 1 }
func (s *purgeDeletedStep) ContinueOnError() bool        { return true }

func (s *Session) discoverDeleted(ctx context.Context) ([]*azcertificates.DeletedCertificateProperties, error) {
	limit := s.maxPurges
	logger := s.logger(ctx)

	var candidates []*azcertificates.DeletedCertificateProperties
	seen := map[string]bool{}
	pager := s.client.NewListDeletedCertificatePropertiesPager(nil)
	for pager.More() {
		if len(candidates) >= limit {
			s.count(func(c *summary) { c.Skipped["purge-limit"]++ })
			break
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list deleted certificate metadata: %w", err)
		}
		for _, cert := range page.Value {
			if len(candidates) >= limit {
				s.count(func(c *summary) { c.Skipped["purge-limit"]++ })
				return candidates, nil
			}
			s.count(func(c *summary) { c.DeletedScanned++ })
			name, selection, decision := s.eligibleDeleted(cert)
			if !decision.Selected() {
				s.count(func(c *summary) { c.Skipped["deleted-"+string(decision)]++ })
				continue
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate deleted certificate %q in inventory", name)
			}
			seen[name] = true
			s.count(func(c *summary) { c.PurgeEligible++; c.PurgeSelected++ })
			candidates = append(candidates, cert)
			logger.Info(
				"Selected deleted CI certificate",
				"name", name,
				"owner", selection.OwnerToken,
				"policyRule", selection.RuleName,
				"reason", fmt.Sprintf("rule[%d]-deleted", selection.RuleIndex),
				"deleted", cert.DeletedDate,
				"scheduledPurge", cert.ScheduledPurgeDate,
				"recoveryID", cert.RecoveryID,
			)
		}
	}
	return candidates, nil
}
