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

package cipatterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

func encodeBatches(rows []resultRow) ([]*bytes.Buffer, error) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ResultKey < rows[j].ResultKey })
	var batches []*bytes.Buffer
	batch := &bytes.Buffer{}
	count := 0
	total := 0
	for _, row := range rows {
		encoded := &bytes.Buffer{}
		if err := json.NewEncoder(encoded).Encode(row); err != nil {
			return nil, fmt.Errorf("encode result %s: %w", row.ResultKey, err)
		}
		if encoded.Len() > maxRowBytes {
			return nil, fmt.Errorf("result %s exceeds %d encoded bytes", row.ResultKey, maxRowBytes)
		}
		total += encoded.Len()
		if total > maxReadBytes {
			return nil, fmt.Errorf("encoded result population exceeds %d bytes", maxReadBytes)
		}
		if count == maxBatchRows || batch.Len()+encoded.Len() > maxBatchBytes {
			batches = append(batches, batch)
			batch = &bytes.Buffer{}
			count = 0
		}
		_, _ = batch.Write(encoded.Bytes())
		count++
	}
	if count > 0 {
		batches = append(batches, batch)
	}
	return batches, nil
}
