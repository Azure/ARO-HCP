materialize:
	@echo "Materializing public cloud int env of config/config.yaml"
	@../templatize.sh dev > public-cloud-dev.json
	@echo "Materializing public cloud cs-pr env of config/config.yaml"
	@../templatize.sh cs-pr > public-cloud-cs-pr.json
	@echo "Materializing nightly env of config/config.yaml"
	@../templatize.sh nightly > public-cloud-nightly.json
	@echo "Materializing public cloud personal dev env of config/config.yaml"
	@USER=tst ../templatize.sh personal-dev > public-cloud-personal-dev.json
	@echo "Materializing public cloud int env of config/config.msft.yaml"
	@CONFIG_FILE=../config/config.msft.yaml ../templatize.sh int > public-cloud-msft-int.json
	@echo "Materializing public cloud stg env of config/config.msft.yaml"
	@CONFIG_FILE=../config/config.msft.yaml ../templatize.sh stg > public-cloud-msft-stg.json
.PHONY: materialize

detect-change: materialize
	@diff_output=$$(git diff -- './*.json'); \
	if [ -n "$$diff_output" ]; then \
		echo "Please review the diffs below:\n\n"; \
		echo "$$diff_output"; \
		echo "\n\n===================================================="; \
		echo "\n\nOnce you reviewed the changes and consider them meaningful, update them by running "make -C config/ materialize" and commit your changes."; \
		echo "\n\n===================================================="; \
		exit 1; \
	else \
		echo "No changes detected in the configuration files."; \
	fi
.PHONY: detect-change
