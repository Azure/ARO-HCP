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

package prow

import (
	"fmt"
	"strconv"
	"time"
)

// Twitter Snowflake epoch used by Prow/Deck build IDs.
const twitterEpochMS int64 = 1288834974657

// BuildIDToTime decodes a Snowflake build ID to a time.Time.
func BuildIDToTime(bid string) (time.Time, error) {
	n, err := strconv.ParseInt(bid, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid build ID %q: %w", bid, err)
	}
	tsMS := (n >> 22) + twitterEpochMS
	return time.UnixMilli(tsMS).UTC(), nil
}

// BuildIDToISO decodes a Snowflake build ID to an ISO timestamp string.
func BuildIDToISO(bid string) (string, error) {
	t, err := BuildIDToTime(bid)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02T15:04:05"), nil
}

// TimeToBuildID encodes a time.Time to the nearest Snowflake build ID.
func TimeToBuildID(t time.Time) string {
	tsMS := t.UnixMilli()
	return strconv.FormatInt((tsMS-twitterEpochMS)<<22, 10)
}

// ISOToBuildID encodes an ISO date or datetime string to a Snowflake build ID.
// Accepts "YYYY-MM-DD" (assumes 00:00:00 UTC) or "YYYY-MM-DDTHH:MM:SS".
func ISOToBuildID(iso string) (string, error) {
	if len(iso) == 10 {
		iso += "T00:00:00"
	}
	t, err := time.Parse("2006-01-02T15:04:05", iso)
	if err != nil {
		return "", fmt.Errorf("invalid ISO timestamp %q: %w", iso, err)
	}
	t = t.UTC()
	return TimeToBuildID(t), nil
}
