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

package cipatterns

import (
	"container/list"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	buildversion "github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

// reconcileInputs is also the fixed-input reconciliation path for the future
// coordinator. It never re-enriches inputs and never treats acceptance as visibility.
func (c *Controller) reconcileInputs(ctx context.Context, version failurePatternVersion, inputs []input) (time.Duration, error) {
	if version != compiledFailurePatternVersion() {
		return 0, fmt.Errorf("cannot compute a target other than the executable contract")
	}
	if err := validateInputPopulation(inputs); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(inputs) == 0 {
		return 0, nil
	}
	rows, err := c.readResults(ctx, inputs, version)
	if err != nil {
		return 0, fmt.Errorf("read visible computations: %w", err)
	}
	visible, err := validatedVisibleResults(inputs, version, rows)
	if err != nil {
		return 0, err
	}
	missing := make([]input, 0, len(inputs))
	keys := make([]string, 0, cap(missing))
	for _, i := range inputs {
		i = prepareInput(i, version)
		if _, ok := visible[i.key]; ok {
			continue
		}
		missing = append(missing, i)
		keys = append(keys, i.key)
	}
	build := inputs[0].observation.BuildID
	if len(missing) == 0 {
		c.metrics.visible.Inc()
		if polls := c.pendingPolls(build); polls > 0 {
			c.logger.Debug("Build computations visible", "buildId", build, "inputs", len(inputs), "visibilityPolls", polls)
		} else {
			c.logger.Debug("Build already complete", "buildId", build, "inputs", len(inputs))
		}
		return 0, nil
	}
	sort.Strings(keys)
	cacheKey := strings.Join(keys, "\n")
	if c.batches.contains(cacheKey, c.now()) {
		c.metrics.cacheHits.Inc()
		c.logger.Debug("Awaiting visibility of submitted computations", "buildId", build, "missing", len(missing))
		return c.retryDelay, nil
	}
	computed := make([]resultRow, 0, len(missing))
	producerCommit := buildversion.CommitSHA
	for _, i := range missing {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		row := envelope(i, version)
		row.Findings = cifailurepatterns.Extract(cifailurepatterns.Input{Text: row.Message, TestName: row.TestName, Source: row.Source})
		row.ComputedAt = c.now().UTC()
		row.ProducerCommit = producerCommit
		computed = append(computed, row)
	}
	batches, err := encodeBatches(computed)
	if err != nil {
		return 0, err
	}
	for _, payload := range batches {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := c.submit(ctx, payload); err != nil {
			return 0, fmt.Errorf("submit missing computations: %w", err)
		}
		c.metrics.accepted.Inc()
	}
	c.batches.add(cacheKey, c.now())
	c.logger.Debug("Submitted computations", "buildId", build, "inputs", len(inputs), "missing", len(missing), "batches", len(batches))
	return c.retryDelay, nil
}

type cachedSubmission struct {
	key     string
	expires time.Time
}

type submissionCache struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	entries  map[string]*list.Element
	order    *list.List
	bytes    int
}

func newSubmissionCache(capacity int, ttl time.Duration) *submissionCache {
	return &submissionCache{capacity: capacity, ttl: ttl, entries: map[string]*list.Element{}, order: list.New()}
}

func (s *submissionCache) contains(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	element, ok := s.entries[key]
	if !ok {
		return false
	}
	if !now.Before(element.Value.(cachedSubmission).expires) {
		delete(s.entries, key)
		s.order.Remove(element)
		s.bytes -= len(key)
		return false
	}
	s.order.MoveToFront(element)
	return true
}

func (s *submissionCache) add(key string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capacity <= 0 || len(key) > maxReadBytes {
		return
	}
	if previous := s.entries[key]; previous != nil {
		s.order.Remove(previous)
		s.bytes -= len(key)
	}
	s.entries[key] = s.order.PushFront(cachedSubmission{key: key, expires: now.Add(s.ttl)})
	s.bytes += len(key)
	for len(s.entries) > s.capacity || s.bytes > maxReadBytes {
		last := s.order.Back()
		key := last.Value.(cachedSubmission).key
		delete(s.entries, key)
		s.order.Remove(last)
		s.bytes -= len(key)
	}
}
