SHELL = /bin/bash

TENANT_ID=$(shell az account show --query tenantId --output tsv)
MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "{{ .managementClusterRG }}" -n maestro-consumer --query clientId -o tsv)
EVENTGRID_HOSTNAME=$(shell az resource show -n {{ .maestroEventgridName }} -g {{ .regionRG }} --resource-type "Microsoft.EventGrid/namespaces" --query properties.topicSpacesConfiguration.hostname -o tsv)

deploy:
	helm upgrade --install maestro-agent ./helm \
		--create-namespace --namespace maestro \
		--set consumerName={{ .maestroConsumerName }} \
		--set broker.host=${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name={{ .maestroKeyVaultName }} \
		--set credsKeyVault.secret={{ .maestroConsumerName }} \
		--set azure.clientId=${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=${TENANT_ID} \
		--set image.base={{ .maestroImageBase }} \
		--set image.tag={{ .maestroImageTag }}
.PHONY: deploy
