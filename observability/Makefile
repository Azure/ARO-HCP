all: deploy patch-frontend patch-clusterservice
deploy:
	kubectl apply -k deploy/

patch-frontend:
	kubectl patch -n aro-hcp deployment aro-hcp-frontend --type=json --patch-file=patches/otel-sdk.yaml

patch-clusterservice:
	kubectl patch -n cluster-service deployment cluster-service --type=json --patch-file=patches/otel-sdk.yaml
