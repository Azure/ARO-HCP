#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

project_root="$(dirname "${BASH_SOURCE}")/../.."

ACTUAL_OUTPUT_DIR="${project_root}/test/e2e/test-artifacts/generated-test-artifacts"
OUTPUT_DIR="${1:-${ACTUAL_OUTPUT_DIR}}"

# create output directory if it doesn't exist
mkdir -p "${OUTPUT_DIR}/standard-cluster-create"
mkdir -p "${OUTPUT_DIR}/illegal-install-version"
mkdir -p "${OUTPUT_DIR}/image-registry"

# Function to calculate SHA256 hash of a file
calculate_hash() {
    local file="$1"
    sha256sum "$file" | cut -d ' ' -f 1
}

# Function to check if hash matches stored hash
check_hash() {
    local file="$1"
    local hash_file="${file}.sha256"
    if [ ! -f "$hash_file" ]; then
        return 1
    fi
    local current_hash=$(calculate_hash "$file")
    local stored_hash=$(cat "$hash_file")
    [ "$current_hash" = "$stored_hash" ]
}

# Generate customer-infra.json if needed
# Convert bicep file to json and store its hash
convert_bicep_to_json() {
    local bicep_file="$1"
    local json_file="$2"

    # check if the output is missing, if the output file has been modified, or if the input file has been modified
    if [ ! -f "${json_file}" ] || \
       ! check_hash "${json_file}" || \
       ! check_hash "${bicep_file}"; then
        az bicep build --file="${bicep_file}" --outfile="${json_file}"

        json_hash=$(calculate_hash "${json_file}")
        echo "${json_hash}" > "${json_file}.sha256"
        bicep_hash=$(calculate_hash "${bicep_file}")
        echo "${bicep_hash}" > "${bicep_file}.sha256"
    fi
}

# Process existing conversions
convert_bicep_to_json "${project_root}/demo/bicep/customer-infra.bicep" "${OUTPUT_DIR}/standard-cluster-create/customer-infra.json"
convert_bicep_to_json "${project_root}/demo/bicep/cluster.bicep" "${OUTPUT_DIR}/standard-cluster-create/cluster.json"
convert_bicep_to_json "${project_root}/demo/bicep/nodepool.bicep" "${OUTPUT_DIR}/standard-cluster-create/nodepool.json"

convert_bicep_to_json "${project_root}/test/e2e/test-artifacts/illegal-install-version/cluster.bicep" "${OUTPUT_DIR}/illegal-install-version/cluster.json"

convert_bicep_to_json "${project_root}/test/e2e/test-artifacts/image-registry/disabled-image-registry-cluster.bicep" "${OUTPUT_DIR}/image-registry/disabled-image-registry-cluster.json"

# Process e2e-setup bicep files
find "${project_root}/test/e2e-setup/bicep" -type f -name "*.bicep" | while read bicep_file; do
    # Get relative path from bicep root
    rel_path=$(realpath --relative-to="${project_root}/test/e2e-setup/bicep" "$(dirname "$bicep_file")")
    filename=$(basename "$bicep_file")
    json_filename="${filename%.bicep}.json"

    # Create output directory
    output_dir="${OUTPUT_DIR}/e2e-setup/${rel_path}"
    mkdir -p "$output_dir"

    # Convert bicep to json
    convert_bicep_to_json "$bicep_file" "${output_dir}/${json_filename}"
done


