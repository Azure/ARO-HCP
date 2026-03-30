# Redaction Rules

- Quote only the smallest log excerpt needed to support a claim.
- Do not include tokens, kubeconfigs, secrets, or customer-sensitive payloads.
- Prefer identifiers such as alert names, `probe_url`, namespace names, and file paths over raw blobs.
- If a log line contains sensitive fields, summarize it instead of quoting it directly.
