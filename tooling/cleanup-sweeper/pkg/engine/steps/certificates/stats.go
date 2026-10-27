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

type summary struct {
	Scanned                int
	Eligible               int
	Selected               int
	Attempts               int
	Deleted                int
	AlreadyAbsent          int
	Failed                 int
	DeletedScanned         int
	PurgeEligible          int
	PurgeSelected          int
	PurgeAttempts          int
	Purged                 int
	PurgeAlreadyAbsent     int
	PurgeFailed            int
	ImmediatePurgeWaits    int
	ImmediatePurgeDeferred int
	Skipped                map[string]int
}

func newSummary() summary {
	return summary{Skipped: map[string]int{}}
}

func (s *summary) merge(other summary) {
	s.Attempts += other.Attempts
	s.Deleted += other.Deleted
	s.AlreadyAbsent += other.AlreadyAbsent
	s.Failed += other.Failed
	s.PurgeAttempts += other.PurgeAttempts
	s.Purged += other.Purged
	s.PurgeAlreadyAbsent += other.PurgeAlreadyAbsent
	s.PurgeFailed += other.PurgeFailed
	s.ImmediatePurgeWaits += other.ImmediatePurgeWaits
	s.ImmediatePurgeDeferred += other.ImmediatePurgeDeferred
	for reason, count := range other.Skipped {
		s.Skipped[reason] += count
	}
}
