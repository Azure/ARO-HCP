#!/bin/bash

# This is a super simple approach to syncing dashboards
# TODO:
#  - check if dashboard really needs update (will cause many versions in grafana db)
#  - check if stale folder needs to be deleted
#  - check if stale dashboard needs to be deleted 

set -euo pipefail

RESOURCEGROUP=${GLOBAL_RESOURCEGROUP:-global}
DRY_RUN=${DRY_RUN:-false}

if [[ -z ${GRAFANA_NAME} ]];
then
    echo "GRAFANA_NAME needs to be set"
    exit 1
fi

cd dashboards

existing_folders=$(az grafana folder list -g ${RESOURCEGROUP}  -n ${GRAFANA_NAME})

for d in $(find . -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
do
    if [[ $(echo $existing_folders | jq --arg TITLE "$d"  -c '.[] | select( .title == $TITLE )' |wc -l )  -gt 0 ]];
    then
        folderUid=$(echo $existing_folders | jq --arg TITLE "$d"  -r '.[] | select( .title == $TITLE ) | .uid' )
    else
        if [[ ${DRY_RUN} == "true" ]];
        then
            echo "would create folder '${d}' on managed grafana ${GRAFANA_NAME} in rg ${RESOURCEGROUP}"
        else
            folderUid=$(az grafana folder create --only-show-errors  -g ${RESOURCEGROUP}  -n ${GRAFANA_NAME} --title ${d} |jq -r '.uid')
        fi
    fi
    pushd ${d}
    IFS=$'\n'; for dashboard in $(ls -1)
    do
        dashboard_name=$(cat ${dashboard} | jq '.dashboard.title' )
        if [[ ${DRY_RUN} == "true" ]];
        then
            echo "would create/update dashboard '${dashboard_name}' on managed grafana ${GRAFANA_NAME} in rg ${RESOURCEGROUP}"
        else
            if [[ $(grep -c XYZTOBESETBYPIPELINEZYX ${dashboard}) -ne 1 ]];
            then
                echo "Magic string XYZTOBESETBYPIPELINEZYX not found in dashboard file ${dashboard}" >&2
            else
                sed -i "s/XYZTOBESETBYPIPELINEZYX/${folderUid}/" ${dashboard}
                az grafana dashboard update --overwrite true -g ${RESOURCEGROUP}  -n ${GRAFANA_NAME} --definition ${dashboard}
            fi
        fi
    done
    popd
done
