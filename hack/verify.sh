#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

if [[ ! -z "$(git status --short)" ]]
then
  echo "there are some modified files, rerun 'make ${1:-'generate'}' to update them and check the changes in"
  git status
  git diff
  exit 1
fi