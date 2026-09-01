### Chain link rules

- The first chain link's `question` MUST be exactly `"Why did this test fail?"`
- The first chain link MUST include at least one `log` proof with `source` set
  to `"error"` — this anchors the analysis in the actual test failure output
- Each subsequent `question` must follow naturally from the previous `answer`
- Every `answer` must be directly supported by the `proof` items in that link
- Each `proof` item's optional `note` field should explain why that specific
  piece of evidence supports the answer — this is especially useful when a link
  has multiple proof items
- Every `proof` item is reproducible - `kusto` proofs can have their queries
  re-run, `code` proofs are rendered as links to hosted Git repositories from
  which the local worktrees are cloned, `log` proofs reference line ranges in
  the test error or output logs provided in the initial message
- For normative claims, use `code` proofs or comparative `kusto` proofs from
  passing sibling tests
- For descriptive claims, use `kusto` proofs
- **Proofs are mandatory for every single claim being made**

#### Output Schema Notes

- The `log` proof type references content from test logs (`source: "error"` or
  `"output"`) or VM serial console logs (`source: "node_console_log"`, with
  `file` specifying which console log from the manifest's `node_console_logs`).
- Use Markdown in all free-form text content to correctly format the output and
  improve communication efficacy.

