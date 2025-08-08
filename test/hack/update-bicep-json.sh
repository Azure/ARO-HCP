#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

project_root="$(dirname "${BASH_SOURCE}")/../.."

ACTUAL_OUTPUT_DIR="${project_root}/test/e2e/test-artifacts/generated-test-artifacts"
OUTPUT_DIR="${1:-${ACTUAL_OUTPUT_DIR}}"

# create output directory if it doesn't exist
mkdir -p "${OUTPUT_DIR}/standard-cluster-create"

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

demo_bicep_dir="${project_root}/demo/bicep"
std_cluster_bicep_dir="${OUTPUT_DIR}/standard-cluster-create/"

demo_bicep_files=(
  "customer-infra.bicep"
  "cluster.bicep"
  "nodepool.bicep"
)

for bicep_file in ${demo_bicep_files[@]}
do
  convert_bicep_to_json "${demo_bicep_dir}/${bicep_file}" "${std_cluster_bicep_dir}/${bicep_file}"
done

test_bicep_file_paths=(
  "cmk-etcd/cmk-etcd-cluster.bicep"
  "illegal-install-version/cluster.bicep"
  "image-registry/disabled-image-registry-cluster.bicep"
)

test_bicep_dir="${project_root}/test/e2e/test-artifacts"

for bicep_file_path in "${test_bicep_file_paths[@]}"
do
  data=(${bicep_file_path//\// })
  convert_bicep_to_json "${test_bicep_dir}/${data[0]}/${data[1]}" "${OUTPUT_DIR}/${data[0]}/${data[1]%.bicep}.json"
done
