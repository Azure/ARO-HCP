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

package schema

import (
	"strings"
	"testing"
)

type record struct {
	SchemaVersion string            `json:"schemaVersion"`
	Kind          string            `json:"kind"`
	B             int               `json:"b"`
	A             string            `json:"a"`
	Extra         map[string]string `json:"extra,omitempty"`
}

func TestCanonicalIsStableAcrossMapOrder(t *testing.T) {
	first := record{SchemaVersion: Version, Kind: "Test", B: 2, A: "x", Extra: map[string]string{"one": "1", "two": "2"}}
	second := record{SchemaVersion: Version, Kind: "Test", B: 2, A: "x", Extra: map[string]string{"two": "2", "one": "1"}}
	left, err := Canonical(first)
	if err != nil {
		t.Fatalf("Canonical() error = %v", err)
	}
	right, err := Canonical(second)
	if err != nil {
		t.Fatalf("Canonical() error = %v", err)
	}
	if string(left) != string(right) {
		t.Fatalf("canonical forms differ:\n%s\n%s", left, right)
	}
	if !strings.Contains(string(left), `"a":"x"`) || strings.Contains(string(left), " ") {
		t.Fatalf("canonical form is not compact: %s", left)
	}
	firstDigest, err := Digest(first)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	secondDigest, err := Digest(second)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatal("digests must not depend on map ordering")
	}
}

func TestDigestsWithPrefixSeparatesDomains(t *testing.T) {
	first, err := DigestsWithPrefix("one", "value")
	if err != nil {
		t.Fatalf("DigestsWithPrefix() error = %v", err)
	}
	second, err := DigestsWithPrefix("two", "value")
	if err != nil {
		t.Fatalf("DigestsWithPrefix() error = %v", err)
	}
	if first == second {
		t.Fatal("prefixes must change the digest")
	}
}

func TestDecodeStrictRejectsUnknownAndTrailingContent(t *testing.T) {
	valid := `{"schemaVersion":"depctl/v1","kind":"Test","a":"x","b":1}`
	var target record
	if err := DecodeEnvelope([]byte(valid), "Test", &target); err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	cases := map[string]string{
		"unknown field":   `{"schemaVersion":"depctl/v1","kind":"Test","a":"x","b":1,"c":2}`,
		"trailing":        valid + `{}`,
		"wrong version":   `{"schemaVersion":"depctl/v2","kind":"Test","a":"x","b":1}`,
		"wrong kind":      `{"schemaVersion":"depctl/v1","kind":"Other","a":"x","b":1}`,
		"missing version": `{"kind":"Test","a":"x","b":1}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			var decoded record
			if err := DecodeEnvelope([]byte(content), "Test", &decoded); err == nil {
				t.Fatal("expected a decoding error")
			}
		})
	}
}

func TestDecodeStrictBoundsPayload(t *testing.T) {
	oversized := make([]byte, MaxRecordBytes+1)
	for index := range oversized {
		oversized[index] = ' '
	}
	var target record
	if err := DecodeStrict(oversized, &target); err == nil {
		t.Fatal("expected an oversized payload error")
	}
}
