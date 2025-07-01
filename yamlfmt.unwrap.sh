#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

source hack/utils.sh

while IFS= read -r file; do
  # Step 1: Unwrap quotes from specific fields
  os::util::sed -E "s/([pP]ort|replicas|enabled|azCount|expression): '\{\{\s*([^}]+)\s*\}\}'$/\1: {{ \2 }}/g" "$file"
  # Step 2: Clean up leading spaces in unwrapped expressions
  os::util::sed -E "s/: \\{\\{ +/: {{ /g" "$file"
  # Step 3: Clean up trailing spaces in unwrapped expressions
  os::util::sed -E "s/ +\\}\\}$/ }}/g" "$file"
done < <(grep -r -l -E "([pP]ort|replicas|enabled|azCount|expression): '\{\{" --include '*.yaml' --include '*.yml' . || true)
