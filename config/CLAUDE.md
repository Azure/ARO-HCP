Docs on how these configs work:
- docs/configuration.md
- docs/bicep.md if you're editing bicep files

When making changes to `configs/config*.yaml` you might need to update the appropriate schema files.

After updating configs, verify and render new config by running:
- `cd config; make materialize`
These might result in changes to rendered configs which must be attached to any PRs.

Examples:
- Commit 4a276befedc7f8a33692c6d8d6746e22344f195c adds a new bool parameter set to True by default, but False in 'dev' cloud envs. Commit d123a93b4e92ffd15e3c8f930a28c8195bf94cdd has the rendered configs.
