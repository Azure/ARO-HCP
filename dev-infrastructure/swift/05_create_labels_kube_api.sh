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

kubectl -n $NAMESPACE patch deployment kube-apiserver -p '{"spec":{"template":{"metadata":{"labels":{"kubernetes.azure.com/pod-network-instance":"pni1"}}}}}'