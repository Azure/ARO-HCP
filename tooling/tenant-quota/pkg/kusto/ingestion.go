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

package kusto

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-kusto-go/azkustoingest"
)

func IngestJSONLines(ctx context.Context, ingestor *azkustoingest.Ingestion, mapping string, payload io.Reader, extra ...azkustoingest.FileOption) error {
	options := make([]azkustoingest.FileOption, 0, 2+len(extra))
	options = append(options, azkustoingest.IngestionMappingRef(mapping, azkustoingest.MultiJSON), azkustoingest.FileFormat(azkustoingest.MultiJSON))
	options = append(options, extra...)
	if _, err := ingestor.FromReader(ctx, payload, options...); err != nil {
		return fmt.Errorf("ingest JSON lines: %w", err)
	}
	return nil
}
