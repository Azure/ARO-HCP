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
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	cleanuppolicy "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

const defaultDeletedPollInterval = time.Second

// deletedCertificateWait bounds how long a successful delete waits for its tombstone.
const deletedCertificateWait = 30 * time.Second

// Config configures a certificate cleanup Session.
type Config struct {
	Client   Client
	VaultURL string
	Policy   cleanuppolicy.CertificateDiscoveryPolicy
	// Owners is required when any policy rule needs the resource-group owner inventory.
	Owners OwnerInventoryFn

	DryRun       bool
	DeleteActive bool
	PurgeDeleted bool
	// MaxDeletions bounds the active certificates selected for deletion, each of
	// which is purged immediately when PurgeDeleted is set. MaxPurges
	// independently bounds the previously deleted certificates (tombstones) purged.
	MaxDeletions int
	MaxPurges    int
}

// Session is the state shared by the delete and purge steps of one cleanup run:
// the policy, the owner guard, counters and failures.
type Session struct {
	client    Client
	vaultURL  string
	vaultHost string
	policy    cleanuppolicy.CertificateDiscoveryPolicy
	guard     *ownerGuard

	dryRun       bool
	deleteActive bool
	purgeDeleted bool
	maxDeletions int
	maxPurges    int

	now          func() time.Time
	pollInterval time.Duration

	mu       sync.Mutex
	counts   summary
	failures []error
}

// NewSession validates the configuration and builds a Session.
func NewSession(cfg Config) (*Session, error) {
	if cfg.Client == nil {
		return nil, errors.New("certificate client is required")
	}
	vault, err := url.Parse(cfg.VaultURL)
	if err != nil || vault.Scheme != "https" || vault.Host == "" {
		return nil, fmt.Errorf("invalid vault URL %q", cfg.VaultURL)
	}
	if !cfg.DeleteActive && !cfg.PurgeDeleted {
		return nil, errors.New("at least one of delete-active or purge-deleted must be enabled")
	}
	if cfg.DeleteActive && cfg.MaxDeletions <= 0 {
		return nil, errors.New("max deletions must be positive")
	}
	if cfg.PurgeDeleted && cfg.MaxPurges <= 0 {
		return nil, errors.New("max purges must be positive")
	}
	if err := cfg.Policy.Validate("certificates.discovery"); err != nil {
		return nil, err
	}
	// Only active deletions use the owner guard; tombstones are purged without it.
	if cfg.DeleteActive && cfg.Policy.RequiresOwnerInventory() && cfg.Owners == nil {
		return nil, errors.New("owner inventory is required to delete active certificates under this certificate policy")
	}

	s := &Session{
		client:       cfg.Client,
		vaultURL:     cfg.VaultURL,
		vaultHost:    vault.Host,
		policy:       cfg.Policy,
		dryRun:       cfg.DryRun,
		deleteActive: cfg.DeleteActive,
		purgeDeleted: cfg.PurgeDeleted,
		maxDeletions: cfg.MaxDeletions,
		maxPurges:    cfg.MaxPurges,
		now:          time.Now,
		counts:       newSummary(),
	}
	s.guard = &ownerGuard{owners: cfg.Owners, now: func() time.Time { return s.now() }}
	return s, nil
}

// Steps returns the enabled cleanup steps in execution order. Active
// certificates are deleted (and immediately purged) first, so a tombstone left
// behind by a failed or skipped immediate purge is picked up by the tombstone
// purge step of the same run instead of waiting for the next one.
func (s *Session) Steps() []runner.Step {
	var steps []runner.Step
	if s.deleteActive {
		steps = append(steps, &deleteActiveStep{session: s})
	}
	if s.purgeDeleted {
		steps = append(steps, &purgeDeletedStep{session: s})
	}
	return steps
}

// Finish logs the run summary and decides the run result. It is meant to be the
// engine's PostRunFn: individual failures do not abort the run, so partial
// success is only an error when nothing succeeded, the owner guard failed, or
// the context ended.
func (s *Session) Finish(ctx context.Context) error {
	logger := s.logger(ctx)

	s.mu.Lock()
	counts := s.counts
	failures := append([]error(nil), s.failures...)
	s.mu.Unlock()

	logger.Info("CI certificate sweep summary", "counts", counts)
	if len(failures) > 0 {
		logger.Error(errors.Join(failures...), "Certificate cleanup attempts completed with errors", "failed", len(failures))
	}
	if s.dryRun {
		return ctx.Err()
	}
	if joined := errors.Join(failures...); errors.Is(joined, errOwnerInventory) || ctx.Err() != nil {
		return errors.Join(joined, ctx.Err())
	}
	if counts.Deleted+counts.AlreadyAbsent+counts.Purged+counts.PurgeAlreadyAbsent == 0 {
		return errors.Join(failures...)
	}
	return nil
}

func (s *Session) logger(ctx context.Context) logr.Logger {
	return logr.FromContextOrDiscard(ctx).WithValues(
		"vault", s.vaultURL,
		"dryRun", s.dryRun,
		"deleteActive", s.deleteActive,
		"purgeDeleted", s.purgeDeleted,
	)
}

// record merges one target's counters and remembers its failure, if any.
func (s *Session) record(action, name string, counts summary, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts.merge(counts)
	if err != nil && !errors.Is(err, runner.ErrTargetRetained) {
		s.failures = append(s.failures, fmt.Errorf("%s %s: %w", action, name, err))
	}
}

func (s *Session) count(update func(*summary)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.counts)
}
