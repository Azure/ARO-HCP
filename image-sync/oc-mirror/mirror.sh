#!/bin/sh

/usr/local/bin/imageset-config-tmpl -c /config/imageset-config.yml.tmpl -o /config/imageset-config.yml -s ${STABLE_VERSIONS} -r ${REGISTRY_URL}
/usr/local/bin/oc-mirror --continue-on-error --config /config/imageset-config.yml docker://${REGISTRY_URL} @$
