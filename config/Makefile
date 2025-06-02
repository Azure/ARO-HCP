# this is a region used for materializing configuration
# this one has been chosen because the resulting region short name
#  * is 5 characters long
#  * ends with a number
# and thus name length limits and naming patterns can be tested
# this is also in alignment with personal dev environments, where the short region name is a 5 character string
REGION=southwestus

materialize:
	@echo "Materializing public cloud int env of config/config.yaml"
	@REGION=$(REGION) ../templatize.sh dev > public-cloud-dev.json
	@echo "Materializing public cloud cspr env of config/config.yaml"
	@REGION=$(REGION) ../templatize.sh cspr > public-cloud-cspr.json
	@echo "Materializing ntly env of config/config.yaml"
	@REGION=$(REGION) ../templatize.sh ntly > public-cloud-ntly.json
	@echo "Materializing public cloud personal dev env of config/config.yaml"
	@USER=tst REGION=$(REGION) ../templatize.sh pers > public-cloud-pers.json
	@echo "Materializing public cloud int env of config/config.msft.yaml"
	@CONFIG_FILE=../config/config.msft.yaml REGION=$(REGION) ../templatize.sh int > public-cloud-msft-int.json
	@echo "Materializing public cloud stg env of config/config.msft.yaml"
	@CONFIG_FILE=../config/config.msft.yaml REGION=$(REGION) ../templatize.sh stg > public-cloud-msft-stg.json
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
