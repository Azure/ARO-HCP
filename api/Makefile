SHELL = /bin/bash

.PHONY: generate
generate: 
	tsp format --check "**/*.tsp"
	tsp compile redhatopenshift/HcpCluster --warn-as-error
	autorest --verbose autorest-config.yaml 

.PHONY: fmt
fmt:
	tsp format "**/*.tsp"
