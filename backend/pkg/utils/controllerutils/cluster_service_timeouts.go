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

package controllerutils

import "time"

// Default CS operation budgets. Callers should use local budgets when their
// operation's latency or upstream work requires a different bound.
const (
	// About 5x the observed read p99.9 of 3062ms; slow outliers can retry.
	CSReadTimeout = 15 * time.Second
	// Twice the observed write maximum of 7492ms, rounded up.
	CSWriteTimeout = 15 * time.Second
)
