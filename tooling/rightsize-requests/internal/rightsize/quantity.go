// Copyright 2025 Microsoft Corporation
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

package rightsize

import (
	"fmt"
	"strconv"
	"strings"
)

// sentinelValues are non-numeric request values that must never be edited.
var sentinelValues = map[string]struct{}{
	"NONE":      {},
	"unlimited": {},
	"":          {},
}

// IsSentinel reports whether a config value is a non-numeric sentinel that the
// tool must leave untouched (e.g. "NONE" for the self-managed prometheus).
func IsSentinel(v string) bool {
	_, ok := sentinelValues[v]
	return ok
}

// ParseCPUCores parses a Kubernetes CPU quantity (e.g. "100m", "1") into cores.
func ParseCPUCores(s string) (float64, error) {
	if IsSentinel(s) {
		return 0, fmt.Errorf("non-numeric value %q", s)
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "m") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "m"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid cpu quantity %q: %w", s, err)
		}
		return n / 1000.0, nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cpu quantity %q: %w", s, err)
	}
	return n, nil
}

// memSuffixes maps Kubernetes memory unit suffixes to byte multipliers.
// Binary (power-of-two) suffixes are listed before decimal ones.
var memSuffixes = []struct {
	suffix string
	mult   float64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50},
	{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15},
}

// ParseMemoryBytes parses a Kubernetes memory quantity (e.g. "512Mi", "1Gi",
// "1000000") into bytes.
func ParseMemoryBytes(s string) (float64, error) {
	if IsSentinel(s) {
		return 0, fmt.Errorf("non-numeric value %q", s)
	}
	s = strings.TrimSpace(s)
	for _, u := range memSuffixes {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err != nil {
				return 0, fmt.Errorf("invalid memory quantity %q: %w", s, err)
			}
			return n * u.mult, nil
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory quantity %q: %w", s, err)
	}
	return n, nil
}

// FormatCPU renders CPU cores as a Kubernetes millicore string, rounded UP to
// the nearest 10m and never below 10m. Rounding is always UP so the emitted
// request is never smaller than the observed usage.
func FormatCPU(cores float64) string {
	milli := ceilInt(cores * 1000.0) // ceil to whole millicores first
	milli = roundUpDivFloor(milli, 10) * 10
	if milli < 10 {
		milli = 10
	}
	return fmt.Sprintf("%dm", milli)
}

// FormatMemory renders bytes as a Kubernetes binary quantity, rounded UP to the
// nearest 16Mi, collapsing to whole Gi when exactly divisible.
func FormatMemory(bytes float64) string {
	const mi = 1024 * 1024
	mib := ceilInt(bytes / mi) // ceil to whole MiB first
	// round up to nearest 16Mi
	if r := mib % 16; r != 0 {
		mib += 16 - r
	}
	if mib < 16 {
		mib = 16
	}
	if mib%1024 == 0 {
		return fmt.Sprintf("%dGi", mib/1024)
	}
	return fmt.Sprintf("%dMi", mib)
}

// ceilInt returns the ceiling of v as an int64, tolerating tiny floating-point
// error (e.g. 0.1*1000 == 100.00000000000001) so exact multiples do not
// spuriously round up.
func ceilInt(v float64) int64 {
	i := int64(v)
	if v-float64(i) > 1e-9 {
		i++
	}
	return i
}

// roundUpDivFloor returns ceil(n/d) for positive integers.
func roundUpDivFloor(n, d int64) int64 {
	if n <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
