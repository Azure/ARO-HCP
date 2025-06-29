
winget install --id=Kubernetes.kubectl  -e
winget install --id=Microsoft.Azure.Kubelogin  -e
az aks get-credentials --overwrite-existing --only-show-errors -n int-uksouth-mgmt-1 -g hcp-underlay-int-uksouth-mgmt-1 -f "int-uksouth-mgmt-1.kubeconfig"
kubelogin convert-kubeconfig -l azurecli --kubeconfig "int-uksouth-mgmt-1.kubeconfig"


$env:KUBECONFIG = "int-uksouth-mgmt-1.kubeconfig"
