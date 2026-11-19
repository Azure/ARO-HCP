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

package dependabot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
)

// NativeReadFunc reads a fixed official release GET through the caller's authenticated proxy.
type NativeReadFunc func(context.Context, string, bool, bool) (json.RawMessage, error)

// NativePolicyError identifies rejected untrusted candidate evidence.
type NativePolicyError struct {
	Reason string
}

// Error returns the policy rejection.
func (e *NativePolicyError) Error() string {
	return e.Reason
}

// NativeReject creates a policy rejection.
func nativeReject(reason string) error {
	return &NativePolicyError{Reason: reason}
}

var nativeSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var nativeReleaseEndpoint = regexp.MustCompile(`^repos/[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*/commits/[A-Za-z0-9%_.~-]+$`)

// NativeDecode rejects duplicate keys, trailing JSON and non-integer identity coercion.
func nativeDecode(raw []byte) (any, error) {
	check := json.NewDecoder(bytes.NewReader(raw))
	if err := validateNativeJSON(check, 0); err != nil {
		return nil, err
	}
	if _, err := check.Token(); err != io.EOF {
		return nil, nativeReject("trailing JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// NativeRead restricts callbacks to fixed official release commit GETs.
func nativeRead(ctx context.Context, read NativeReadFunc, endpoint string, pages, ci bool) (any, error) {
	if read == nil || pages || ci || !nativeReleaseEndpoint.MatchString(endpoint) {
		return nil, nativeReject("unsupported official release GET")
	}
	raw, err := read(ctx, endpoint, false, false)
	if err != nil {
		return nil, err
	}
	return nativeDecode(raw)
}

// NativeObject requires non-null object metadata.
func nativeObject(value any) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, nativeReject("missing JSON object")
	}
	return object, nil
}

// NativeInteger accepts exact decoded JSON integers only.
func nativeInteger(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	integer, err := number.Int64()
	return integer, err == nil
}
