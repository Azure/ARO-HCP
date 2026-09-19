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

# Compiles demo/bicep and test/e2e-setup/bicep with the pinned `bicep`
# binary and diffs the result (compiler version/templateHash metadata
# stripped) against the committed golden fixtures in
# hack/bicep-golden-fixtures/. A real diff here means the pinned bicep
# version (or a bicep source change) altered compiled output, not just the
# compiler's own version string, and needs review before being accepted:
# run hack/generate-bicep-golden.sh and commit the result.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOLDEN_DIR="$REPO_ROOT/hack/bicep-golden-fixtures"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

make -C "$REPO_ROOT/test" BICEP_OUTPUT_DIR="$tmpdir/" bicep-fixtures

strip_metadata() {
  jq 'walk(if type == "object" and has("_generator") and (._generator|type) == "object" then ._generator |= del(.version, .templateHash) else . end)' "$1"
}

fail=0

while IFS= read -r -d '' fresh; do
  rel="${fresh#"$tmpdir"/}"
  golden="$GOLDEN_DIR/$rel"
  if [[ ! -f "$golden" ]]; then
    echo "ERROR: $rel was generated from a bicep source but has no golden fixture at $golden"
    echo "  run hack/generate-bicep-golden.sh and commit the new file"
    fail=1
    continue
  fi
  if ! diff -u "$golden" <(strip_metadata "$fresh") >/tmp/bicep-fixture-diff.$$.txt; then
    echo "ERROR: bicep-compiled output for $rel differs from the golden fixture beyond compiler version/templateHash metadata:"
    cat /tmp/bicep-fixture-diff.$$.txt
    echo "  run hack/generate-bicep-golden.sh, review the diff, and commit the change if it is expected"
    fail=1
  fi
  rm -f /tmp/bicep-fixture-diff.$$.txt
done < <(find "$tmpdir" -name '*.json' -print0)

while IFS= read -r -d '' golden; do
  rel="${golden#"$GOLDEN_DIR"/}"
  if [[ ! -f "$tmpdir/$rel" ]]; then
    echo "ERROR: golden fixture $rel no longer has a matching bicep source"
    echo "  run hack/generate-bicep-golden.sh and commit the removal"
    fail=1
  fi
done < <(find "$GOLDEN_DIR" -name '*.json' -print0)

exit "$fail"
