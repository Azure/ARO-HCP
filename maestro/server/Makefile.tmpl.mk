SHELL = /bin/bash

TENANT_ID=$(shell az account show --query tenantId --output tsv)
MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "{{ .serviceClusterRG }}" -n maestro-server --query clientId -o tsv)
EVENTGRID_HOSTNAME=$(shell az resource show -n {{ .maestroEventgridName }} -g {{ .regionRG }} --resource-type "Microsoft.EventGrid/namespaces" --query properties.topicSpacesConfiguration.hostname -o tsv)
ISTO_VERSION=$(shell az aks show -n {{ .aksName }} -g {{ .serviceClusterRG }} --query serviceMeshProfile.istio.revisions[-1] -o tsv)

deploy:
	kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f -
	kubectl label namespace maestro "istio.io/rev=${ISTO_VERSION}" --overwrite=true
	helm upgrade --install maestro-server ./helm \
		--namespace maestro \
		--set broker.host=${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name={{ .maestroKeyVaultName }} \
		--set azure.clientId=${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=${TENANT_ID} \
		--set istio.restrictIngress={{ .maestroRestrictIstioIngress }} \
		--set image.base={{ .maestroImageBase }} \
		--set image.tag={{ .maestroImageTag }} \
		--set database.containerizedDb={{ not .maestroPostgresDeploy }} \
		--set database.ssl='{{ ternary "enable" "disable" .maestroPostgresDeploy }}'
.PHONY: deploy
