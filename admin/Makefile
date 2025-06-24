-include ../setup-env.mk

CURRENT_COMMIT ?= $(shell COMMIT_SHA=$$(git rev-parse --short=7 HEAD); if [ -z "$$(git status --porcelain 2>/dev/null)" ]; then echo "$$COMMIT_SHA"; else echo "$$COMMIT_SHA"-dirty; fi )
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_ADMIN_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(ARO_HCP_IMAGE_REPOSITORY)

.DEFAULT_GOAL := admin

frontend:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${CURRENT_COMMIT}" -o aro-hcp-admin .

info:
	@echo "ARO_HCP_ADMIN_IMAGE: ${ARO_HCP_ADMIN_IMAGE}"
	@echo "COMMIT: ${CURRENT_COMMIT}"

run:
	./aro-hcp-admin --location ${LOCATION} 
.PHONY: run

clean:
	rm -f aro-hcp-admin
.PHONY: clean

build-push: image push

image:
	@bash -c "set -e; pushd .. > /dev/null; \
	          trap 'rm --force image-environment && popd > /dev/null' EXIT; \
	          cp ${ENV_VARS_FILE} image-environment; \
	          docker build . --file admin/Dockerfile \
	                         --build-arg CURRENT_COMMIT=${CURRENT_COMMIT} \
	                         --build-arg PLATFORM=linux/amd64 \
	                         --tag ${ARO_HCP_ADMIN_IMAGE}:${CURRENT_COMMIT}"
.PHONY: image

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_ADMIN_IMAGE}:${CURRENT_COMMIT}

deploy:
	DIGEST=$$(../get-digest.sh ${ARO_HCP_IMAGE_ACR} aro-hcp-admin) \
	kubectl create namespace admin --dry-run=client -o json | kubectl apply -f - && \
	../hack/helm.sh aro-hcp-admin-dev deploy admin \
	--set deployment.imageName=${ARO_HCP_ADMIN_IMAGE}@$${DIGEST} \
	--set location=westus3
	--namespace admin
.PHONY: deploy

undeploy:
	helm uninstall aro-hcp-admin-dev --namespace admin
.PHONY: undeploy