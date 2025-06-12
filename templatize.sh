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
    echo "  -c          Set the cloud"
    echo "  -r          Set the region"
    echo "  -x          Set the cxstamp"
    echo "  -e          Extra args for config interpolation"
    echo "  -p          Pipeline to inspect"
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

# Extract defaults based on deployment environment
DEPLOY_ENV_DEFAULTS=$( "${PROJECT_ROOT_DIR}/tooling/templatize/settings.sh" "${DEPLOY_ENV}" ".defaults" ) || {
    echo "Failed to get deployment environment defaults from settings.sh" >&2
    exit 1
}
if [ -z "${CLOUD+x}" ]; then
    CLOUD=$(yq -r '.cloud' <<< "${DEPLOY_ENV_DEFAULTS}")
fi
if [ -z "${REGION+x}" ]; then
    REGION=$(yq -r '.region' <<< "${DEPLOY_ENV_DEFAULTS}")
fi
if [ -z "${CXSTAMP+x}" ]; then
    CXSTAMP=$(yq -r '.cxStamp' <<< "${DEPLOY_ENV_DEFAULTS}")
fi
REGION_STAMP_TEMPLATE=$(yq -r '.regionStampTemplate' <<< "${DEPLOY_ENV_DEFAULTS}")

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
        c)
            CLOUD=${OPTARG}
            ;;
        d)
            DRY_RUN="--dry-run"
            ;;
        r)
            REGION=${OPTARG}
            ;;
        x)
            CXSTAMP=${OPTARG}
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
            PIPELINE=${OPTARG}
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

# Read region name from our sanitized serviceconfig.json and returns the region short name
REGION_SHORT=$(
    "${PROJECT_ROOT_DIR}/tooling/templatize/serviceconfig-get-region-short-name.sh" "${REGION}"
)
if [ -z "$REGION_SHORT" ]; then
    echo "Failed to get region short name for region: $REGION" >&2
    exit 1
fi

# Generate region stamp based by expanding the template from the defaults
REGION_STAMP=$(eval "echo \"$REGION_STAMP_TEMPLATE\"")

make -s -C "${PROJECT_ROOT_DIR}/tooling/templatize" templatize
TEMPLATIZE="${PROJECT_ROOT_DIR}/tooling/templatize/templatize"

PERSIST_FLAG=""
if [ -z "${PERSIST+x}" ] || [ "${PERSIST+x}" == "false" ]; then
    PERSIST_FLAG="--no-persist-tag"
fi

CONFIG_FILE=${CONFIG_FILE:-${PROJECT_ROOT_DIR}/config/config.yaml}
if [ -n "${INPUT+x}" ] && [ -n "${OUTPUT+x}" ]; then
    $TEMPLATIZE generate \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --input=${INPUT} \
        --output=${OUTPUT} \
        ${LOG_VERBOSITY_OPTION} \
        ${EXTRA_ARGS}
elif [ $PIPELINE_MODE == "inspect" ] && [ -n "${PIPELINE+x}" ] && [ -n "${PIPELINE_STEP+x}" ]; then
    $TEMPLATIZE pipeline inspect \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --pipeline-file=${PIPELINE} \
        --step=${PIPELINE_STEP} \
        --output=${OUTPUT} \
        --scope vars \
        ${LOG_VERBOSITY_OPTION} \
        --format makefile
elif [ $PIPELINE_MODE == "run" ] && [ -n "${PIPELINE+x}" ] && [ -n "${PIPELINE_STEP+x}" ]; then
    $TEMPLATIZE pipeline run \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --pipeline-file=${PIPELINE} \
        --step=${PIPELINE_STEP} \
        ${PERSIST_FLAG} \
        ${LOG_VERBOSITY_OPTION} \
        ${DRY_RUN}
elif [ $PIPELINE_MODE == "run" ] && [ -n "${PIPELINE+x}" ]; then
    $TEMPLATIZE pipeline run \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --pipeline-file=${PIPELINE} \
        ${PERSIST_FLAG} \
        ${LOG_VERBOSITY_OPTION} \
        ${DRY_RUN}
else
    $TEMPLATIZE inspect \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        ${LOG_VERBOSITY_OPTION} \
        ${EXTRA_ARGS}
fi
