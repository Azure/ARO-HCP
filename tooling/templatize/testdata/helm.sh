# copy from maestro/Makefile#L14
deploy-server:
    TENANT_ID="{{ .tenantId }}"
    REGION_RG="{{ .region_resourcegroup }}"
    EVENTGRID_NS="{{ .region_eventgrid_namespace }}"
    MAESTRO_KV="{{ .region_maestro_keyvault }}"
    SERVICE_RG="{{ .svc_resourcegroup }}"
    AKS="{{ .aks_name }}"
    MAESTRO_MI="{{ .maestro_msi }}"
    HELM_CHART="{{ .maestro_helm_chart }}"

    EVENTGRID_HOSTNAME=$(az event namespace show -g "${REGION_RG}" -n "${EVENTGRID_NS}" --query "properties.topicSpacesConfiguration.hostname")
    MAESTRO_MI_CLIENT_ID=$(az identity show -g "${SERVICE_RG}" -n "${MAESTRO_MI}" --query "clientId")
    ISTO_VERSION=$(az aks show -g "${SERVICE_RG}" -n "${AKS}" --query "serviceMeshProfile.istio.revisions[-1]")

    kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f - && \
    kubectl label namespace maestro "istio.io/rev=${ISTO_VERSION}" --overwrite=true && \
    helm upgrade --install maestro-server "${HELM_CHART}" \
        --namespace maestro \
        --set broker.host=${EVENTGRID_HOSTNAME} \
        --set credsKeyVault.name=${MAESTRO_KV} \
        --set azure.clientId=${MAESTRO_MI_CLIENT_ID} \
        --set azure.tenantId=${TENANT_ID} \
        --set image.base='quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro'\
        --set database.containerizedDb=true \
        --set database.ssl=disable
