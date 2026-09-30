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
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	cleanuppolicy "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// Decisions that depend on Key Vault tombstone metadata rather than on policy
// rules, so they are decided here instead of in the policy package.
const (
	decisionInvalidRecoveryID        cleanuppolicy.CertificateDecision = "invalid-recovery-id"
	decisionNotPurgeable             cleanuppolicy.CertificateDecision = "not-purgeable"
	decisionMissingDeletionTimestamp cleanuppolicy.CertificateDecision = "missing-deletion-timestamp"
)

func (s *Session) selectName(
	name string,
	tags map[string]*string,
	owners sets.Set[string],
	deleted bool,
	now time.Time,
	attributes *azcertificates.CertificateAttributes,
) (cleanuppolicy.CertificateSelection, cleanuppolicy.CertificateDecision) {
	metadata := cleanuppolicy.CertificateMetadata{Name: name, Tags: tags}
	if attributes != nil {
		metadata.Created = attributes.Created
		metadata.Updated = attributes.Updated
	}
	if deleted {
		return s.policy.SelectDeleted(metadata)
	}
	return s.policy.SelectActive(metadata, owners, now)
}

func (s *Session) eligible(
	cert *azcertificates.CertificateProperties,
	owners sets.Set[string],
	now time.Time,
) (string, cleanuppolicy.CertificateSelection, cleanuppolicy.CertificateDecision) {
	if cert == nil {
		return "", cleanuppolicy.CertificateSelection{}, cleanuppolicy.CertificateDecisionInvalidID
	}
	name, _, valid := certificateID(s.vaultHost, cert.ID)
	if !valid {
		return "", cleanuppolicy.CertificateSelection{}, cleanuppolicy.CertificateDecisionInvalidID
	}
	selection, decision := s.selectName(name, cert.Tags, owners, false, now, cert.Attributes)
	return name, selection, decision
}

func (s *Session) eligibleDeleted(
	cert *azcertificates.DeletedCertificateProperties,
) (string, cleanuppolicy.CertificateSelection, cleanuppolicy.CertificateDecision) {
	if cert == nil {
		return "", cleanuppolicy.CertificateSelection{}, cleanuppolicy.CertificateDecisionInvalidID
	}
	name, _, valid := certificateID(s.vaultHost, cert.ID)
	if !valid {
		return "", cleanuppolicy.CertificateSelection{}, cleanuppolicy.CertificateDecisionInvalidID
	}
	selection, decision := s.selectName(name, cert.Tags, nil, true, time.Time{}, cert.Attributes)
	if !decision.Selected() {
		return name, selection, decision
	}
	recoveryName, valid := deletedCertificateID(s.vaultHost, cert.RecoveryID)
	if !valid || recoveryName != name {
		return name, selection, decisionInvalidRecoveryID
	}
	if cert.Attributes == nil || cert.Attributes.RecoveryLevel == nil || !strings.Contains(*cert.Attributes.RecoveryLevel, "Purgeable") {
		return name, selection, decisionNotPurgeable
	}
	if cert.DeletedDate == nil || cert.DeletedDate.IsZero() || cert.ScheduledPurgeDate == nil || cert.ScheduledPurgeDate.IsZero() {
		return name, selection, decisionMissingDeletionTimestamp
	}
	return name, selection, cleanuppolicy.CertificateDecisionSelected
}

// purgeDeletedTarget revalidates one tombstone and purges it. Tombstones are not
// subject to the owner guard: the certificate is already gone, and a lingering
// tombstone would make a new owner of the same name fail on create. It returns
// runner.ErrTargetRetained when the tombstone is no longer eligible.
func (s *Session) purgeDeletedTarget(ctx context.Context, cert *azcertificates.DeletedCertificateProperties) (summary, error) {
	counts := newSummary()
	if err := s.purgeCertificate(ctx, cert, &counts); err != nil {
		counts.PurgeFailed++
		return counts, err
	}
	if counts.Purged+counts.PurgeAlreadyAbsent == 0 {
		return counts, runner.ErrTargetRetained
	}
	return counts, nil
}

// deleteActiveTarget revalidates, soft-deletes and, when enabled, immediately
// purges one active certificate. It returns runner.ErrTargetRetained when the
// certificate is no longer eligible.
func (s *Session) deleteActiveTarget(ctx context.Context, cert *azcertificates.CertificateProperties) (summary, error) {
	counts := newSummary()
	name, _, _ := certificateID(s.vaultHost, cert.ID)
	var owners sets.Set[string]
	var ownersAt time.Time
	if s.policy.RequiresOwnerInventory() {
		var err error
		owners, ownersAt, err = s.guard.current(ctx)
		if err != nil {
			counts.Failed++
			return counts, err
		}
	}
	deleted, err := s.deleteCertificate(ctx, cert, owners, ownersAt, &counts)
	if err != nil {
		counts.Failed++
		return counts, err
	}
	if !deleted {
		return counts, runner.ErrTargetRetained
	}
	if !s.purgeDeleted {
		return counts, nil
	}

	// Purge as soon as the tombstone appears; see purgeDeletedTarget.
	counts.ImmediatePurgeWaits++
	deletedCert, err := s.waitForDeletedCertificate(ctx, name)
	if err != nil {
		counts.PurgeFailed++
		counts.ImmediatePurgeDeferred++
		return counts, err
	}
	if err := s.purgeCertificate(ctx, deletedCert, &counts); err != nil {
		counts.PurgeFailed++
		counts.ImmediatePurgeDeferred++
		return counts, err
	}
	return counts, nil
}

func (s *Session) waitForDeletedCertificate(ctx context.Context, name string) (*azcertificates.DeletedCertificateProperties, error) {
	waitCtx, cancel := context.WithTimeout(ctx, deletedCertificateWait)
	defer cancel()

	interval := s.pollInterval
	if interval <= 0 {
		interval = defaultDeletedPollInterval
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("wait for soft-delete tombstone: %w", waitCtx.Err())
		case <-timer.C:
		}

		latest, err := s.client.GetDeletedCertificate(waitCtx, name, nil)
		if err == nil {
			cert := deletedProperties(latest.DeletedCertificate)
			latestName, _, decision := s.eligibleDeleted(cert)
			if !decision.Selected() || latestName != name {
				return nil, fmt.Errorf("soft-delete tombstone failed eligibility revalidation: %s", decision)
			}
			return cert, nil
		}
		if !isNotFound(err) {
			return nil, fmt.Errorf("get soft-delete tombstone: %w", err)
		}
		timer.Reset(interval)
	}
}

func (s *Session) purgeCertificate(ctx context.Context, cert *azcertificates.DeletedCertificateProperties, counts *summary) error {
	name, _, _ := certificateID(s.vaultHost, cert.ID)
	latest, err := s.client.GetDeletedCertificate(ctx, name, nil)
	if isNotFound(err) {
		counts.PurgeAlreadyAbsent++
		return nil
	}
	if err != nil {
		return fmt.Errorf("revalidate deleted certificate: %w", err)
	}
	current := deletedProperties(latest.DeletedCertificate)
	latestName, _, decision := s.eligibleDeleted(current)
	if !decision.Selected() || latestName != name ||
		!sameString(cert.RecoveryID, latest.RecoveryID) ||
		!sameTime(cert.DeletedDate, latest.DeletedDate) ||
		!sameTime(cert.ScheduledPurgeDate, latest.ScheduledPurgeDate) {
		counts.Skipped["deleted-certificate-revalidation"]++
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	counts.PurgeAttempts++
	_, err = s.client.PurgeDeletedCertificate(ctx, name, nil)
	switch {
	case isNotFound(err):
		counts.PurgeAlreadyAbsent++
		return nil
	case err != nil:
		return fmt.Errorf("purge: %w", err)
	default:
		counts.Purged++
		return nil
	}
}

func (s *Session) deleteCertificate(ctx context.Context, cert *azcertificates.CertificateProperties, owners sets.Set[string], ownersAt time.Time, counts *summary) (bool, error) {
	name, version, _ := certificateID(s.vaultHost, cert.ID)
	selection, decision := s.selectName(name, cert.Tags, owners, false, s.now(), cert.Attributes)
	if !decision.Selected() {
		counts.Skipped["policy-revalidation"]++
		return false, nil
	}
	if selection.OwnerToken != "" && owners.Has(selection.OwnerToken) {
		counts.Skipped["owner-revalidation"]++
		return false, nil
	}
	latest, err := s.client.GetCertificate(ctx, name, "", nil)
	if isNotFound(err) {
		counts.AlreadyAbsent++
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("revalidate: %w", err)
	}
	current := &azcertificates.CertificateProperties{ID: latest.ID, Attributes: latest.Attributes, Tags: latest.Tags, X509Thumbprint: latest.X509Thumbprint}
	latestName, _, decision := s.eligible(current, owners, s.now())
	_, latestVersion, _ := certificateID(s.vaultHost, latest.ID)
	// List IDs are commonly unversioned. In that case exact latest timestamps
	// (and the thumbprint when present) are the snapshot identity, not version history.
	if !decision.Selected() || latestName != name || latestVersion == "" || (version != "" && version != latestVersion) ||
		!cert.Attributes.Created.Equal(*latest.Attributes.Created) || !cert.Attributes.Updated.Equal(*latest.Attributes.Updated) ||
		(len(cert.X509Thumbprint) > 0 && !bytes.Equal(cert.X509Thumbprint, latest.X509Thumbprint)) {
		counts.Skipped["certificate-revalidation"]++
		return false, nil
	}
	if selection.OwnerToken != "" && s.now().Sub(ownersAt) >= ownerMaxAge {
		return false, errors.New("owner inventory expired during certificate revalidation")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	counts.Attempts++
	_, err = s.client.DeleteCertificate(ctx, name, nil)
	switch {
	case isNotFound(err):
		counts.AlreadyAbsent++
		return false, nil
	case err != nil:
		return false, fmt.Errorf("soft-delete: %w", err)
	default:
		counts.Deleted++
		return true, nil
	}
}
