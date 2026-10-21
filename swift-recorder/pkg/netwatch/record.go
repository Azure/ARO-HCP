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

//go:build linux

package netwatch

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Reason names why a Record was emitted. Every Record carries exactly one.
type Reason string

const (
	ReasonChange   Reason = "change"   // Observations is non-empty
	ReasonPeriodic Reason = "periodic" // state snapshot: heartbeat, establishment, status transition
)

// SectionStatus reports whether tracked link state is currently trustworthy.
// Available-and-empty (no selected links) is distinct from Available=false:
// unavailable state is always omitted from State, never presented as a
// stale-but-current inventory.
type SectionStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Record is the structured, loggable node-state record emitted via LogTo.
type Record struct {
	Reason                  Reason    `json:"reason"`
	SessionID               string    `json:"session_id"`
	Sequence                uint64    `json:"sequence"`
	ObservedAt              time.Time `json:"observed_at"`
	EmittedAt               time.Time `json:"emitted_at"`
	LastAppliedNotification uint64    `json:"last_applied_notification"`

	Status SectionStatus `json:"status"`
	State  []LinkState   `json:"state,omitempty"`

	Observations []Observation `json:"observations,omitempty"`
	Incomplete   string        `json:"incomplete,omitempty"`
}

const maxRecordBytes = 65536

// Emitter assigns session identity and sequence to every Record and enforces
// maxRecordBytes against the real logging path before handing a Record to
// sink. It is not safe for concurrent use.
type Emitter struct {
	sessionID string
	sequence  uint64
	sink      func(Record)
}

func NewEmitter(sink func(Record)) *Emitter {
	return &Emitter{sessionID: uuid.NewString(), sink: sink}
}

func (e *Emitter) Emit(r Record) {
	e.sequence++
	r.SessionID, r.Sequence = e.sessionID, e.sequence
	raw, err := json.Marshal(r)
	if err == nil && len(raw) <= maxRecordBytes {
		e.sink(r)
		return
	}
	detail := fmt.Sprintf("%d bytes exceeds %d byte budget", len(raw), maxRecordBytes)
	if err != nil {
		detail = fmt.Sprintf("marshal error: %v", err)
	}
	e.sink(Record{
		Reason: r.Reason, SessionID: r.SessionID, Sequence: r.Sequence,
		ObservedAt: r.ObservedAt, EmittedAt: r.EmittedAt,
		LastAppliedNotification: r.LastAppliedNotification,
		Status:                  r.Status,
		Incomplete:              "record too large to emit: " + detail,
	})
}
