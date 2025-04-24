#
# Generated Pipeline Documentation
#
pipelines.md: ./../topology.yaml
	$(MAKE) -C ./../tooling/pipeline-documentation/ pipeline-documentation
	./../tooling/pipeline-documentation/pipeline-documentation overview --topology ./../topology.yaml --output $@
