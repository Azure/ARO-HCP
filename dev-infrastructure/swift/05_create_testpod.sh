set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

if ! is_redhat_user; then
    az login
fi

kubectl apply -f - << EOF 
apiVersion: v1
kind: Pod
metadata:
  labels:
    kubernetes.azure.com/pod-network-instance: pni1
  name: nginx-swift-test
  namespace: default
spec:
  containers:
  - image: nginx:latest
    imagePullPolicy: Always
    name: nginx-swift
    ports:
    - containerPort: 80
EOF