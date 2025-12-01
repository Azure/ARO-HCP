set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

if ! is_redhat_user; then
    az login
fi

if [ $# -lt 1 ]; then
  echo "$0 takes a single namepace name for an argument"
  exit 1
elif [ $# -eq 1 ]; then
  NAMESPACE=$1
fi

kubectl get pods --selector=app=kube-apiserver -n $NAMESPACE --no-headers | awk '{print $1}' | xargs -I {} kubectl label pod {} kubernetes.azure.com/pod-network-instance=pni1 -n $NAMESPACE