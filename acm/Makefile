SHELL = /bin/bash

deploy:
	./scripts/install-olm.sh v0.28.0
	./scripts/install-acm.sh

create-chart:
	go run ../tooling/mcerepkg/main.go \
		-b quay.io/stolostron/cmb-operator-bundle:2.7.0-BACKPLANE-2024-09-30-05-39-18 \
		-o deploy/helm -s scaffold

.PHONY: deploy create-chart
