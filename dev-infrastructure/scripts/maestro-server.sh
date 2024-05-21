#!/bin/bash

oc process --local -f https://raw.githubusercontent.com/openshift-online/maestro/main/templates/db-template.yml \
    DATABASE_SERVICE_NAME=maestro-db \
    DATABASE_NAME=maestro \
    DATABASE_USER=maestro \
    DATABASE_PASSWORD=maestro | oc apply -f - -n maestro
oc process --local -f https://raw.githubusercontent.com/openshift-online/maestro/main/templates/service-template-aro-hcp.yml \
    IMAGE_REGISTRY=quay.io \
    IMAGE_REPOSITORY=redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro \
    IMAGE_TAG=0ba050b00ef480cf2da6bd937a0ca3711c51644d \
    DB_SSLMODE=disable \
    ENABLE_HTTPS=false \
    DB_SECRET_NAME=maestro-db \
    ENABLE_OCM_MOCK=true \
    ENABLE_JWT=false  | oc apply -f - -n maestro
