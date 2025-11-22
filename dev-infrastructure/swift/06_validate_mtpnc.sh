set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

if [ $# -lt 1 ]; then
  echo "$0 takes a single namepace name for an argument"
  exit 1
elif [ $# -eq 1 ]; then
  NAMESPACE=$1
fi

mapfile -t KAS_PODS < <( kubectl get pods --selector=app=kube-apiserver -n $NAMESPACE --no-headers | awk '{print $1}' )

for pod in ${KAS_PODS[*]}; do
    kubectl get mtpnc $pod -n $NAMESPACE -o yaml
done