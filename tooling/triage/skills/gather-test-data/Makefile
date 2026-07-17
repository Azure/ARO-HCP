include ./../../Makefile

run: $(TRIAGE)
	$(TRIAGE) failing-tests --environment $(ENVIRONMENT) --since $(SINCE) --output-dir $(OUTPUT_DIR)
.PHONY: run