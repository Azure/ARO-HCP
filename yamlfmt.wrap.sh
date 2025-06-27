#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

source "hack/utils.sh"

while IFS= read -r file; do
  # Step 1: Wrap with quotes
  os::util::sed -E "s/(:|-) \{\{\s*([^}]+)\s*\}\}$/\1 '{{ \2 }}'/g" "$file"
  # Step 2: Remove leading spaces inside quotes
  os::util::sed -E "s/'\\{\\{ +/'{{ /g" "$file"
  # Step 3: Remove trailing spaces inside quotes
  os::util::sed -E "s/ +\\}\\}'/ }}'/g" "$file"
done < <(grep -r -l -E '(:|-) \{\{\s*[^}]+\s*\}\}$' --include '*.yaml' --include '*.yml' .)
