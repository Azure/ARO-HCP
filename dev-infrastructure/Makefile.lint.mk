fmt:
	set -e; \
	FILES="$$(find . -type f \( -name "*.bicep" -o -name "*.bicepparam" \) ! -name "*.tmpl.bicepparam")"; \
	for file in $$FILES; do \
	echo "az bicep format --file $${file}"; \
	az bicep format --file $$file; \
	done
.PHONY: fmt

lint:
	set -e; \
	FILES="$$(find . -type f \( -name "*.bicep" -o -name "*.bicepparam" \) ! -name "*.tmpl.bicepparam")"; \
	for file in $$FILES; do \
	echo "az bicep lint --file $${file}"; \
	az bicep lint --file $$file; \
	done
.PHONY: lint
