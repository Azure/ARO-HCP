SHELL = /bin/bash

deploy:
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-20 | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
	  -p PROVISION_SHARDS_CONFIG="cHJvdmlzaW9uX3NoYXJkczoKLSBpZDogMQogIGh5cGVyc2hpZnRfY29uZmlnOiB8CiAgICBhcGlWZXJzaW9uOiB2MQogICAga2luZDogQ29uZmlnCiAgICBjbHVzdGVyczoKICAgIC0gbmFtZTogZGVmYXVsdAogICAgICBjbHVzdGVyOgogICAgICAgIHNlcnZlcjogaHR0cDovL2xvY2FsaG9zdAogICAgdXNlcnM6CiAgICAtIG5hbWU6IGRlZmF1bHQKICAgICAgdXNlcjoKICAgICAgICB0b2tlbjogZm9vCiAgICBjb250ZXh0czoKICAgIC0gbmFtZTogZGVmYXVsdAogICAgICBjb250ZXh0OgogICAgICAgIGNsdXN0ZXI6IGRlZmF1bHQKICAgICAgICB1c2VyOiBkZWZhdWx0CiAgICBjdXJyZW50LWNvbnRleHQ6IGRlZmF1bHQKICBzdGF0dXM6IGFjdGl2ZQogIHJlZ2lvbjogZWFzdHVzCiAgY2xvdWRfcHJvdmlkZXI6IGF6dXJlCiAgdG9wb2xvZ3k6IGRlZGljYXRlZAo=" | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p IMAGE_REGISTRY=devarohcp.azurecr.io \
	  -p IMAGE_REPOSITORY=cluster-service/uhc-clusters-service \
	  -p IMAGE_TAG=1719430575 | oc apply -f -

.PHONY: deploy
