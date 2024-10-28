#!/bin/sh

echo ${IMAGE_SET_CONFIG} | base64 -d /config/imageset-config.yml
/usr/local/bin/oc-mirror --continue-on-error --config /config/imageset-config.yml docker://${REGISTRY_URL} @$
