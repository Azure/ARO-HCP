#!/bin/bash

PROJECT_ROOT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")

# Default values
CLOUD="${CLOUD:-public}"
REGION="${REGION:-westus3}"
CXSTAMP="${CXSTAMP:-1}"
EXTRA_ARGS=""
PIPELINE_MODE="inspect"
DRY_RUN=""

# Function to display usage
usage() {
    echo "Usage: $0 deploy_env input output [-c cloud] [-r region] [-x cxstamp] [-e]"
    echo "  deploy_env  Deployment environment"
    echo "  input       Optional input file"
    echo "  output      Optional output file"
    echo "  -d          Dry run"
    echo "  -i          Set the input file same as second arg"
    echo "  -o          Set the output file same as third arg"
    echo "  -c          Set the cloud (default: public)"
    echo "  -r          Set the region (default: westus3)"
    echo "  -x          Set the cxstamp (default: 1)"
    echo "  -e          Extra args for config interpolation"
    echo "  -p          Pipeline to inspect"
    echo "  -s          Pipeline step to inspect"
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

# short names from EV2 prod ServiceConfig
case ${REGION} in
    eastus)
        REGION_SHORT="use"
        ;;
    westus)
        REGION_SHORT="usw"
        ;;
    centralus)
        REGION_SHORT="usc"
        ;;
    northcentralus)
        REGION_SHORT="usnc"
        ;;
    southcentralus)
        REGION_SHORT="ussc"
        ;;
    westus2)
        REGION_SHORT="usw2"
        ;;
    westus3)
        REGION_SHORT="usw3"
        ;;
    uksouth)
        REGION_SHORT="ln"
        ;;
    *)
        echo "unsupported region: ${REGION}"
        exit 1
esac

if [ "$DEPLOY_ENV" == "personal-dev" ]; then
    REGION_STAMP="${REGION_SHORT}${USER:0:4}"
elif [ "$DEPLOY_ENV" == "personal-perfscale" ]; then
    REGION_STAMP="${REGION_SHORT}p${USER:0:4}"
elif [ "$DEPLOY_ENV" == "dev" ] || [ "$DEPLOY_ENV" == "cs-pr" ]; then
    CLEAN_DEPLOY_ENV=$(echo "${DEPLOY_ENV}" | tr -cd '[:alnum:]')
    REGION_STAMP="${CLEAN_DEPLOY_ENV}"
else
    REGION_STAMP=${REGION_SHORT}
fi

make -s -C ${PROJECT_ROOT_DIR}/tooling/templatize templatize
TEMPLATIZE="${PROJECT_ROOT_DIR}/tooling/templatize/templatize"

PERSIST_FLAG=""
if [ -z "$PERSIST" ] || [ "$PERSIST" == "false" ]; then
    PERSIST_FLAG="--no-persist-tag"
fi

CONFIG_FILE=${CONFIG_FILE:-${PROJECT_ROOT_DIR}/config/config.yaml}
if [ -n "$INPUT" ] && [ -n "$OUTPUT" ]; then
    $TEMPLATIZE generate \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --input=${INPUT} \
        --output=${OUTPUT} \
        ${EXTRA_ARGS}
elif [ $PIPELINE_MODE == "inspect" ] && [ -n "$PIPELINE" ] && [ -n "$PIPELINE_STEP" ]; then
    $TEMPLATIZE pipeline inspect \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --pipeline-file=${PIPELINE} \
        --step=${PIPELINE_STEP} \
        --scope vars \
        --format makefile
elif [ $PIPELINE_MODE == "run" ] && [ -n "$PIPELINE" ] && [ -n "$PIPELINE_STEP" ]; then
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
        ${DRY_RUN}
elif [ $PIPELINE_MODE == "run" ] && [ -n "$PIPELINE" ]; then
    $TEMPLATIZE pipeline run \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        --pipeline-file=${PIPELINE} \
        ${PERSIST_FLAG} \
        ${DRY_RUN}
else
    $TEMPLATIZE inspect \
        --config-file=${CONFIG_FILE} \
        --cloud=${CLOUD} \
        --deploy-env=${DEPLOY_ENV} \
        --region=${REGION} \
        --region-short=${REGION_STAMP} \
        --stamp=${CXSTAMP} \
        ${EXTRA_ARGS}
fi
