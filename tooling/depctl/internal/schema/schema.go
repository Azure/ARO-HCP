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

// Package schema holds the versioned envelope, canonical encoding and strict
// decoding shared by every depctl record.
package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Version is the record schema version written by this build. A record with a
// different version is refused; future versions are never guessed at.
const Version = "depctl/v1"

// MaxRecordBytes bounds every record depctl decodes from a file or adapter.
const MaxRecordBytes = 16 << 20

// Envelope carries the fields that identify a versioned record.
type Envelope struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          string `json:"kind"`
}

// ErrUnsupportedVersion reports a record this build cannot interpret.
var ErrUnsupportedVersion = errors.New("unsupported record schema version")

// CheckVersion validates the envelope of an already decoded record.
func CheckVersion(envelope Envelope, wantKind string) error {
	if envelope.SchemaVersion != Version {
		return fmt.Errorf("%w: %q (this build writes %q)", ErrUnsupportedVersion, envelope.SchemaVersion, Version)
	}
	if envelope.Kind != wantKind {
		return fmt.Errorf("record kind %q is not %q", envelope.Kind, wantKind)
	}
	return nil
}

// DecodeStrict decodes exactly one bounded JSON document, rejecting unknown
// fields, trailing content and documents larger than MaxRecordBytes.
// Decodes exactly one bounded JSON document, rejecting unknown
// fields, trailing content and documents larger than MaxRecordBytes.
func DecodeStrict(data []byte, target any) error {
	if len(data) > MaxRecordBytes {
		return fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode record: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("decode record: trailing content after the JSON document")
	}
	return nil
}

// DecodeEnvelope decodes and version-checks a record of the expected kind.
func DecodeEnvelope(data []byte, kind string, target any) error {
	if err := DecodeStrict(data, target); err != nil {
		return err
	}
	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
		Kind          string `json:"kind"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode record envelope: %w", err)
	}
	return CheckVersion(Envelope(envelope), kind)
}

// Canonical returns the canonical JSON encoding of value: object keys sorted,
// no insignificant whitespace, numbers preserved as written. Identity digests
// are computed over this form so that irrelevant ordering cannot change an ID.
// Returns the canonical JSON encoding of value: object keys sorted,
// no insignificant whitespace, numbers preserved as written. Identity digests
// are computed over this form so that irrelevant ordering cannot change an ID.
func Canonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	var buffer bytes.Buffer
	if err := writeCanonical(&buffer, generic); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Writes the canonical JSON form of a decoded value.
func writeCanonical(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return fmt.Errorf("canonicalize: %w", err)
			}
			buffer.Write(encoded)
			buffer.WriteByte(':')
			if err := writeCanonical(buffer, typed[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
		return nil
	case []any:
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonical(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
		return nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Errorf("canonicalize: %w", err)
		}
		buffer.Write(encoded)
		return nil
	}
}

// Digest returns "sha256:<hex>" over the canonical encoding of value.
func Digest(value any) (string, error) {
	encoded, err := Canonical(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DigestsWithPrefix digests several values in order under a domain prefix so that
// unrelated record shapes cannot collide.
// Digests several values in order under a domain prefix so that
// unrelated record shapes cannot collide.
func DigestsWithPrefix(prefix string, values ...any) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n", prefix)
	for _, value := range values {
		encoded, err := Canonical(value)
		if err != nil {
			return "", err
		}
		hash.Write(encoded)
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
