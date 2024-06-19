SHELL = /bin/bash

deploy:
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-20 | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p IMAGE_REGISTRY=devarohcp.azurecr.io \
	  -p IMAGE_REPOSITORY=uhc-clusters-service \
	  -p IMAGE_TAG=1718802609 | oc apply -f -

.PHONY: deploy
