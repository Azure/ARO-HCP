YAMLFMT_LINK := $(GOBIN)/yamlfmt
$(YAMLFMT_LINK): $(YAMLFMT)
	@echo "creating symlink for $(YAMLFMT) at $(YAMLFMT_LINK)"
	@rm -f $(YAMLFMT_LINK)
	@ln -s $(YAMLFMT) $(YAMLFMT_LINK)

ORAS_LINK := $(GOBIN)/oras
$(ORAS_LINK): $(ORAS)
	@echo "creating symlink for $(ORAS) at $(ORAS_LINK)"
	@rm -f $(ORAS_LINK)
	@ln -s $(ORAS) $(ORAS_LINK)
