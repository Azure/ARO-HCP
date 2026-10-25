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

package azureutil

import (
	"fmt"
	"strconv"
)

// PositiveInt is a pflag.Value that rejects values below 1 when set.
type PositiveInt struct {
	Target  *int
	Default int
}

func (p *PositiveInt) String() string {
	if p.Target == nil || *p.Target < 1 {
		return fmt.Sprint(p.Default)
	}
	return fmt.Sprint(*p.Target)
}

func (p *PositiveInt) Type() string { return "int" }

func (p *PositiveInt) Set(value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid integer %q", value)
	}
	if parsed < 1 {
		return fmt.Errorf("must be at least 1, got %d", parsed)
	}
	*p.Target = parsed
	return nil
}
