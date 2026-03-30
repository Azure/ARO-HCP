PYTHON ?= python3
.RECIPEPREFIX := >

.PHONY: pycheck validate smoke

pycheck:
>@tmpdir=$$(mktemp -d); \
>PYTHONPYCACHEPREFIX="$$tmpdir" $(PYTHON) -m py_compile common/tools/validate_sre_assets.py common/tools/smoke_sre_agent.py; \
>rm -rf "$$tmpdir"

validate: pycheck
>PYTHONDONTWRITEBYTECODE=1 $(PYTHON) common/tools/validate_sre_assets.py

smoke: validate
>PYTHONDONTWRITEBYTECODE=1 $(PYTHON) common/tools/smoke_sre_agent.py $(if $(FIXTURE),--fixture $(FIXTURE),)
