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

// Package probe executes bounded, credential-free router diagnostics in a
// dedicated child process. Discovery and scheduling belong to the caller.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/capture"
)

const (
	// MaxTargets bounds the submitted plan and the engine's active probes.
	MaxTargets = 128
	// MaxRequestBytes also bounds stdin decoding in the router-probe command.
	MaxRequestBytes = 256 * 1024
	// MaxResultBytes bounds each child result; callers may split it into records.
	MaxResultBytes = 1024 * 1024
	// TrustIgnition identifies the external ignition endpoint CA bundle.
	TrustIgnition = "ignition"
	// TrustRoot identifies the internal ignition and KAS service CA bundle.
	TrustRoot      = "root"
	probeTimeout   = 5 * time.Second
	processTimeout = 15 * time.Second
)

// Target is one independently dialed destination. Address and SourceIP are
// literal IPs; ServerName supplies TLS SNI and the HTTP Host header, not DNS.
type Target struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	Address    string `json:"address"`
	SourceIP   string `json:"sourceIP"`
	ServerName string `json:"serverName"`
	// TrustBundle is an identifier, never certificate or private key data.
	TrustBundle string `json:"trustBundle"`
	Path        string `json:"path"`
	Port        int    `json:"port"`
	PlainHTTP   bool   `json:"plainHTTP"`
	// TCPOnly connects without sending or reading application data. It is
	// restricted to worker-outbound port 22 with a discovered Swift source.
	TCPOnly bool `json:"tcpOnly"`
	// LivenessOnFailure adds a diagnostic /livez?exclude=etcd probe only after
	// a completed, TLS-verified /readyz response with status >=500. It is
	// restricted to HTTPS kas-* targets using TrustRoot.
	LivenessOnFailure bool `json:"livenessOnFailure"`
}

// DNSConfig is the discovered resolver configuration, never the helper's own
// resolv.conf. Searches and Options are evidence only: queries use DNSNames
// verbatim, without search expansion, fallback, or retries.
type DNSConfig struct {
	Servers  []string `json:"servers"`
	Searches []string `json:"searches"`
	Options  []string `json:"options"`
	Source   string   `json:"source"`
}

// Request pins discovery to a namespace identity. SwiftIPs is the allowlist
// for explicit source binding; an empty Target.SourceIP uses kernel selection.
type Request struct {
	NamespacePath   string    `json:"namespacePath"`
	NamespaceDevice uint64    `json:"namespaceDevice"`
	NamespaceInode  uint64    `json:"namespaceInode"`
	Targets         []Target  `json:"targets"`
	DNS             DNSConfig `json:"dns"`
	DNSNames        []string  `json:"dnsNames"`
	SwiftIPs        []string  `json:"swiftIPs"`
	// TrustBundles contains only PEM public CA certificates, keyed by identifier.
	// Only TrustRoot and TrustIgnition are loaded, at most 64 KiB of PEM each
	// and 128 KiB in total, excluding identifiers.
	// Missing or invalid bundles fail only the HTTPS targets that need them.
	TrustBundles map[string]string `json:"trustBundles"`
}

// Observation records one connection's stages. Durations are milliseconds.
// Error never contains response bodies, response headers or certificates.
type Observation struct {
	Target      Target             `json:"target"`
	StartedAt   time.Time          `json:"startedAt"`
	DurationMS  float64            `json:"durationMs"`
	Stage       string             `json:"stage"`
	Error       string             `json:"error,omitempty"`
	Source      string             `json:"source,omitempty"`
	Destination string             `json:"destination,omitempty"`
	Timings     map[string]float64 `json:"timingsMs"`
	TLS         bool               `json:"tls"`
	// TLSVerify reports that verification is required, not that it succeeded.
	TLSVerify             bool           `json:"tlsVerify"`
	TLSVerified           bool           `json:"tlsVerified,omitempty"`
	HTTPStatus            int            `json:"httpStatus,omitempty"`
	ExpectedStatus        int            `json:"expectedStatus"`
	Expected200           bool           `json:"expected200"`
	BodyBytesDiscarded    int64          `json:"bodyBytesDiscarded"`
	BodyTruncated         bool           `json:"bodyTruncated"`
	BodyTruncationReason  string         `json:"bodyTruncationReason,omitempty"`
	PreliminaryRoute      map[string]any `json:"preliminaryRoute,omitempty"`
	PreliminaryRouteError string         `json:"preliminaryRouteError,omitempty"`
	// Route is a post-connect kernel lookup using Source and Destination,
	// including both TCP ports. It is evidence, not proof of the packet path.
	Route      map[string]any `json:"route,omitempty"`
	RouteError string         `json:"routeError,omitempty"`
	// TCPOutcome is set only for TCP-only connection attempts: connected,
	// refused, timeout, bindRoutingError, or otherError. Refused is evidence
	// of a returned rejection, not worker health or a successful connection.
	TCPOutcome string `json:"tcpOutcome,omitempty"`
	// Liveness is a separate connection, never a replacement for readiness.
	// Its target has LivenessOnFailure cleared to prevent recursive follow-ups.
	Liveness *Observation `json:"liveness,omitempty"`
}

// DNSObservation describes a single A/AAAA question sent once over UDP or TCP.
type DNSObservation struct {
	Name        string    `json:"name"`
	Server      string    `json:"server"`
	Protocol    string    `json:"protocol"`
	Type        string    `json:"type"`
	StartedAt   time.Time `json:"startedAt"`
	DurationMS  float64   `json:"durationMs"`
	Stage       string    `json:"stage"`
	Error       string    `json:"error,omitempty"`
	Source      string    `json:"source,omitempty"`
	Destination string    `json:"destination,omitempty"`
	RCode       int       `json:"rcode"`
	Truncated   bool      `json:"truncated"`
	Answers     []string  `json:"answers,omitempty"`
}

// Result contains before/after namespace evidence as well as active probes.
// Truncated reports input/output omissions; Before/After.Truncated and each
// DNS observation report their own collection bounds. The engine retains at
// most MaxTargets targets, 4 resolver servers and 8 DNS names, with explicit omissions.
type Result struct {
	NamespaceDevice uint64                       `json:"namespaceDevice"`
	NamespaceInode  uint64                       `json:"namespaceInode"`
	StartedAt       time.Time                    `json:"startedAt"`
	FinishedAt      time.Time                    `json:"finishedAt"`
	Targets         []Observation                `json:"targets"`
	DNSConfig       DNSConfig                    `json:"dnsConfig"`
	DNS             []DNSObservation             `json:"dns"`
	Before          Evidence                     `json:"before"`
	After           Evidence                     `json:"after"`
	CounterDeltas   map[string]map[string]uint64 `json:"counterDeltas,omitempty"`
	Truncated       []string                     `json:"truncated,omitempty"`
}

// Evidence is a bounded, thread-local snapshot. Errors and Truncated distinguish
// missing evidence from an empty kernel table. Counters are namespace-wide, not
// attributable exclusively to these probes.
type Evidence struct {
	At        time.Time                    `json:"at"`
	State     map[string]any               `json:"state,omitempty"`
	Listeners map[string][]string          `json:"listeners,omitempty"`
	RPFilter  map[string]string            `json:"rpFilter,omitempty"`
	Counters  map[string]map[string]uint64 `json:"counters,omitempty"`
	// CounterReads is true only for a complete, successful proc counter read.
	CounterReads map[string]bool   `json:"counterReads,omitempty"`
	Errors       map[string]string `json:"errors,omitempty"`
	Truncated    []string          `json:"truncated,omitempty"`
}

// Execute invokes the hidden router-probe command with bounded JSON stdin and
// stdout. Cancellation or the 15-second deadline kills and reaps the helper.
// A failed or oversized helper never returns a partial JSON document.
func Execute(ctx context.Context, executable string, req Request) (json.RawMessage, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRequestBytes {
		return nil, fmt.Errorf("probe request exceeded %d bytes", MaxRequestBytes)
	}
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "router-probe")
	cmd.Stdin = bytes.NewReader(data)
	stdout := limitedOutput{limit: MaxResultBytes, cancel: cancel}
	stderr := limitedOutput{limit: 16 * 1024, cancel: cancel}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	capture.LogCleanupErrors(ctx, stderr.data)
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("probe helper output exceeded limit")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("probe helper: %w", err)
	}
	if !json.Valid(stdout.data) {
		return nil, fmt.Errorf("probe helper returned invalid JSON")
	}
	return json.RawMessage(stdout.data), nil
}

type limitedOutput struct {
	data     []byte
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, b.limit-len(b.data))
	b.data = append(b.data, p[:keep]...)
	if keep < n {
		b.exceeded = true
		b.cancel()
	}
	return n, nil
}

func writeResult(output io.Writer, result Result) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(data)+1 > MaxResultBytes {
		result.Before, result.After = Evidence{}, Evidence{}
		result.Truncated = append(result.Truncated, "supplementary evidence omitted: result exceeds 1 MiB")
		data, err = json.Marshal(result)
		if err != nil {
			return err
		}
	}
	if len(data)+1 > MaxResultBytes {
		return fmt.Errorf("probe result exceeded %d bytes", MaxResultBytes)
	}
	_, err = output.Write(append(data, '\n'))
	return err
}
