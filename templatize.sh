#!/bin/bash
set -euo pipefail

PROJECT_ROOT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")

EXTRA_ARGS=""
PIPELINE_MODE="inspect"
DRY_RUN=""
LOG_LEVEL="${LOG_LEVEL:-5}"
LOG_VERBOSITY_OPTION="-v ${LOG_LEVEL}"

# Function to display usage
usage() {
    echo "Usage: $0 deploy_env input output [-c cloud] [-r region] [-x cxstamp] [-e]"
    echo "  deploy_env  Deployment environment"
    echo "  input       Optional input file"
    echo "  output      Optional output file"
    echo "  -d          Dry run"
    echo "  -i          Set the input file same as second arg"
    echo "  -o          Set the output file same as third arg"
    echo "  -r          Override the region for an environment"
    echo "  -e          Extra args for config interpolation"
    echo "  -p          Pipeline to inspect, identified by service group"
    echo "  -s          Pipeline step to inspect"
    exit 1
}

# Check if at least one positional argument is provided
if [ "$#" -lt 1 ]; then
    usage
fi

# Extract deployment environment
DEPLOY_ENV=$1
shift

if [ "$#" -ge 1 ] && [[ ! "$1" =~ ^- ]]; then
    INPUT=$1
    shift
fi

if [ "$#" -ge 1 ] && [[ ! "$1" =~ ^- ]]; then
    OUTPUT=$1
    shift
fi

# Parse optional flags
while getopts "c:dr:x:e:i:o:p:P:s:" opt; do
    case ${opt} in
        d)
            DRY_RUN="--dry-run"
            ;;
        r)
            REGION=${OPTARG}
            ;;
        e)
            EXTRA_ARGS="--extra-args ${OPTARG}"
            ;;
        i)
            INPUT=${OPTARG}
            ;;
        o)
            OUTPUT=${OPTARG}
            ;;
        p)
            SERVICE_GROUP=${OPTARG}
            ;;
        P)
            PIPELINE_MODE=${OPTARG}
            ;;
        s)
            PIPELINE_STEP=${OPTARG}
            ;;
        *)
            usage
            ;;
    esac
done

make -s -C ${PROJECT_ROOT_DIR}/tooling/templatize templatize
TEMPLATIZE="${PROJECT_ROOT_DIR}/tooling/templatize/templatize"

PERSIST_FLAG=""
if [ -z "${PERSIST+x}" ] || [ "${PERSIST+x}" == "false" ]; then
    PERSIST_FLAG="--no-persist-tag"
fi

CONFIG_FILE=${CONFIG_FILE:-${PROJECT_ROOT_DIR}/config/config.yaml}
if [ -n "${INPUT+x}" ] && [ -n "${OUTPUT+x}" ]; then
    $TEMPLATIZE generate \
        --config-file="${CONFIG_FILE}" \
        --dev-settings-file="${PROJECT_ROOT_DIR}/tooling/templatize/settings.yaml" \
        --dev-environment="${DEPLOY_ENV}" "${REGION:+"--region=${REGION}"}"\
        --input="${INPUT}" \
        --output="${OUTPUT}" \
        ${LOG_VERBOSITY_OPTION} \
        ${EXTRA_ARGS}
elif [ $PIPELINE_MODE == "inspect" ] && [ -n "${SERVICE_GROUP+x}" ] && [ -n "${PIPELINE_STEP+x}" ]; then
    $TEMPLATIZE pipeline inspect \
        --config-file="${CONFIG_FILE}" \
        --dev-settings-file="${PROJECT_ROOT_DIR}/tooling/templatize/settings.yaml" \
        --dev-environment="${DEPLOY_ENV}" "${REGION:+"--region=${REGION}"}" \
        --topology-file="${PROJECT_ROOT_DIR}/topology.yaml" \
        --service-group="${SERVICE_GROUP}" \
        --step="${PIPELINE_STEP}" \
        --output="${OUTPUT}" \
        --scope vars \
        ${LOG_VERBOSITY_OPTION} \
        --format makefile
elif [ $PIPELINE_MODE == "run" ] && [ -n "${SERVICE_GROUP+x}" ] && [ -n "${PIPELINE_STEP+x}" ]; then
    TOPOLOGY_FILE="${TOPOLOGY_FILE:-${PROJECT_ROOT_DIR}/topology.yaml}"
    $TEMPLATIZE pipeline run \
        --config-file="${CONFIG_FILE}" \
        --dev-settings-file="${PROJECT_ROOT_DIR}/tooling/templatize/settings.yaml" \
        --dev-environment="${DEPLOY_ENV}" "${REGION:+"--region=${REGION}"}" \
        --topology-file="${TOPOLOGY_FILE}" \
        --service-group="${SERVICE_GROUP}" \
        --step="${PIPELINE_STEP}" \
        ${PERSIST_FLAG} \
        ${LOG_VERBOSITY_OPTION} \
        ${DRY_RUN}
elif [ $PIPELINE_MODE == "run" ] && [ -n "${SERVICE_GROUP+x}" ]; then
    TOPOLOGY_FILE="${TOPOLOGY_FILE:-${PROJECT_ROOT_DIR}/topology.yaml}"
    $TEMPLATIZE pipeline run \
        --config-file="${CONFIG_FILE}" \
        --dev-settings-file="${PROJECT_ROOT_DIR}/tooling/templatize/settings.yaml" \
        --dev-environment="${DEPLOY_ENV}" "${REGION:+"--region=${REGION}"}" \
        --topology-file="${TOPOLOGY_FILE}" \
        --service-group="${SERVICE_GROUP}" \
        ${PERSIST_FLAG} \
        ${LOG_VERBOSITY_OPTION} \
        ${DRY_RUN}
else
    $TEMPLATIZE inspect \
        --config-file="${CONFIG_FILE}" \
        --dev-settings-file="${PROJECT_ROOT_DIR}/tooling/templatize/settings.yaml" \
        --dev-environment="${DEPLOY_ENV}" "${REGION:+"--region=${REGION}"}" \
        ${LOG_VERBOSITY_OPTION} \
        ${EXTRA_ARGS}
fi
