#!/usr/bin/env bash

# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Regenerates the committed bicep compile-regression golden fixtures under
# hack/bicep-golden-fixtures/ from demo/bicep and test/e2e-setup/bicep, using
# the pinned `bicep` binary. Compiler version/templateHash metadata is
# stripped so the golden files only change when compiled output actually
# changes. Run this after a legitimate bicep source or BICEP_VERSION change,
# review the diff, and commit the result.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOLDEN_DIR="$REPO_ROOT/hack/bicep-golden-fixtures"

find "$GOLDEN_DIR" -mindepth 1 -not -name README.md -delete
make -C "$REPO_ROOT/test" BICEP_OUTPUT_DIR="$GOLDEN_DIR/" bicep-fixtures

while IFS= read -r -d '' f; do
  jq 'walk(if type == "object" and has("_generator") and (._generator|type) == "object" then ._generator |= del(.version, .templateHash) else . end)' "$f" > "$f.tmp"
  mv "$f.tmp" "$f"
done < <(find "$GOLDEN_DIR" -name '*.json' -print0)

echo "Regenerated $(find "$GOLDEN_DIR" -name '*.json' | wc -l) golden fixtures under $GOLDEN_DIR"
echo "Review the diff and commit if the change is expected."
