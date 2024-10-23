#!/bin/bash

set -e

DEPLOY_ENV=$1
cd $(dirname "$(realpath "${BASH_SOURCE[0]}")") || exit
../templatize.sh "$DEPLOY_ENV" config.tmpl.mk config.mk
for tmpl_file in configurations/*.tmpl.*; do
    output_file="${tmpl_file/.tmpl/}"
    ../templatize.sh "$DEPLOY_ENV" "$tmpl_file" "$output_file"
done
