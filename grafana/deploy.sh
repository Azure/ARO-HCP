#!/bin/bash

for folder in $(az grafana folder list  -g jboll-global  -n jboll-arohcp-dev  |jq -r '.[].uid') 
do
    echo "deleting folder $folder"
    az grafana folder delete -g jboll-global  -n jboll-arohcp-dev --folder ${folder}
done

cd dashboards

for d in $(ls -1)
do
    folderUid=$(az grafana folder create  -g jboll-global  -n jboll-arohcp-dev --title ${d} |jq -r '.uid')
    cd ${d}
    for dashboard in $(ls -1)
    do
        sed -i "s/XYZTOBESETBYPIPELINEZYX/${folderUid}/" ${dashboard}
        az grafana dashboard update -g jboll-global  -n jboll-arohcp-dev --definition ${dashboard}
    done
    cd -
done



