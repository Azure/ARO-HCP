SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
include Makefile.deploy

backend:
	go build -o aro-hcp-backend .

run:
	./aro-hcp-backend --location ${LOCATION} \
		--clusters-service-url http://localhost:8000

clean:
	rm -f aro-hcp-backend

image:
	pushd .. && git archive --output backend/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_BACKEND_IMAGE} .
	rm -f archive.tar.gz

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_BACKEND_IMAGE}

undeploy:
	helm uninstall aro-hcp-backend-dev --namespace aro-hcp

.PHONY: backend run clean image push undeploy
