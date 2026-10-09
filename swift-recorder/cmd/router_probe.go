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

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

func runRouterProbe(input io.Reader, output io.Writer) error {
	const limit = probe.MaxRequestBytes
	data, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || len(data) > limit {
		return fmt.Errorf("router probe request unavailable or too large")
	}
	var request probe.Request
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("invalid router probe request")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid trailing router probe request data")
	}
	return probe.Run(request, output)
}
