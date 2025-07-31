#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

PROJECT_ROOT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")
make -s -C "${PROJECT_ROOT_DIR}/tooling/yamlwrap" yamlwrap
while IFS= read -r file; do
  tooling/yamlwrap/yamlwrap unwrap --input "$file"
done < <(find . -type f \( -name '*.yaml' -o -name '*.yml' \))
