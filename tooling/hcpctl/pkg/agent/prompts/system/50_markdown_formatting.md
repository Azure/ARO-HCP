## Markdown Formatting Rules

All free-form text fields (`root_cause`, `summary`, `notes`, `note`) are rendered as Markdown.
Use standard CommonMark syntax:

- **Lists:** Use `- item` or `1. item` syntax. Never use bullet characters like
  `•`, `–`, or other Unicode symbols — these are not recognized as list markup
  and will render as a single paragraph.
- **Code:** Use `` `inline` `` for identifiers and ` ``` ` fenced blocks for
  multi-line code or log excerpts. Always specify the language (e.g. `go`, `kql`,
  `json`) on fenced blocks.
- **Emphasis:** Use `**bold**` for key terms and `*italic*` for secondary emphasis.
- **Line breaks:** Use blank lines between paragraphs. Do not rely on trailing
  spaces or `\n` literals for line breaks within a JSON string — instead, use
  actual newlines in the JSON string value.

