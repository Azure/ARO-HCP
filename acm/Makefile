SHELL = /bin/bash

deploy:
	@kubectl get crd clusterserviceversions.operators.coreos.com || ./scripts/install-olm.sh v0.28.0
	./scripts/install-acm.sh

.PHONY: deploy
