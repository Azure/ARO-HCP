### Chain link rules

- The first chain link's `question` MUST restate the investigation objective as
  a specific, answerable "why?" (see The Recursive Why Method)
- The first chain link MUST be anchored in at least one concrete piece of
  evidence — a `kusto` result, a `code` excerpt, or a `log` proof — that
  establishes the reported symptom as an observed fact rather than a paraphrase
- Provide a top-level `title`: a short (≤ 10 word) headline describing the
  finding, used as the rendered document heading
- Each subsequent `question` must follow naturally from the previous `answer`
- Every `answer` must be directly supported by the `proof` items in that link
- Each `proof` item's optional `note` field should explain why that specific
  piece of evidence supports the answer — this is especially useful when a link
  has multiple proof items
- Every `proof` item is reproducible - `kusto` proofs can have their queries
  re-run, `code` proofs are rendered as links to hosted Git repositories from
  which the local worktrees are cloned, `log` proofs reference line ranges in
  the logs provided in the initial message (when present)
- For normative claims, use `code` proofs or comparative `kusto` proofs from a
  known-good cluster or a comparable time window
- For descriptive claims, use `kusto` proofs
- **Proofs are mandatory for every single claim being made**

#### Output Schema Notes

- The `log` proof type references content from diagnostic logs when the snapshot
  includes them (`source: "error"` or `"output"`) or VM serial console logs
  (`source: "node_console_log"`, with `file` specifying which console log from
  the manifest's `node_console_logs`). Log proofs are optional — prefer `kusto`
  and `code` proofs when no relevant logs are present in the snapshot.
- Use Markdown in all free-form text content to correctly format the output and
  improve communication efficacy.

