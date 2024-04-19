SHELL = /bin/bash

.PHONY: generate
generate: 
	tsp compile redhatopenshift/HcpCluster --warn-as-error
	autorest autorest-config.yaml 
