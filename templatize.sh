#!/bin/bash

PROJECT_ROOT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")

# Default values
CLOUD="${CLOUD:-public}"
REGION="${REGION:-westus3}"
CXSTAMP="${CXSTAMP:-1}"
EXTRA_ARGS=""

# Function to display usage
usage() {
    echo "Usage: $0 deploy_env input output [-c cloud] [-r region] [-x cxstamp] [-e]"
    echo "  deploy_env  Deployment environment"
    echo "  input       Optional input file"
    echo "  output      Optional output file"
    echo "  -c          Set the cloud (default: public)"
    echo "  -r          Set the region (default: westus3)"
    echo "  -x          Set the cxstamp (default: 1)"
    echo "  -e          Extra args for config interpolation"
    exit 1
}

# Check if at least one positional argument is provided
if [ "$#" -lt 1 ]; then
    usage
fi

# Positional arguments
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
while getopts "c:r:x:e:" opt; do
    case ${opt} in
        c)
            CLOUD=${OPTARG}
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
        *)
            usage
            ;;
    esac
done

if [ "$DEPLOY_ENV" == "personal-dev" ]; then
    REGION_STAMP=${USER}
else
    REGION_STAMP=${DEPLOY_ENV}
fi

TEMPLATIZE=${PROJECT_ROOT_DIR}/tooling/templatize/templatize
if [ ! -f "${TEMPLATIZE}" ] || [ -n "${REBUILD_TEMPLATIZE}" ]; then
    go build -o "$TEMPLATIZE" ${PROJECT_ROOT_DIR}/tooling/templatize
fi

CONFIG_FILE=${PROJECT_ROOT_DIR}/config/config.yaml
if [ -n "$INPUT" ] && [ -n "$OUTPUT" ]; then
    $TEMPLATIZE generate \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-stamp=${REGION_STAMP} \
        --cx-stamp=${CXSTAMP} \
        --input=${INPUT} \
        --output=${OUTPUT} \
        ${EXTRA_ARGS}
else
    $TEMPLATIZE inspect \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-stamp=${REGION_STAMP} \
        --cx-stamp=${CXSTAMP} \
        ${EXTRA_ARGS}
fi
