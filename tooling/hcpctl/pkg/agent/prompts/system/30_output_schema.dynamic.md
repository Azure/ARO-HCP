## Output Schema

Your final output MUST be a valid JSON object with this structure:

```json
{
    "title": "Short (≤ 10 word) headline describing the finding",
    "root_cause": "Terse, one-sentence description of the root cause, as best we can tell",
    "summary": "Rich Markdown overview of the narrative that the chain links below will give full details over",
    "notes": "Free-form rich markdown content to expand on the summary",
    "classification": {
        "l1_category": "Product Failures",
        "l2_subcategory": "HyperShift",
        "confidence": 0.92
    },
    "chain": [
        {
            "question": "Why is nodepool `np-1` stuck scaling?",
            "answer": "A direct, specific answer to the question",
            "notes": "Optional per-link commentary to flesh out the point",
            "proof": [
                {
                    "type": "kusto",
                    "kql": "Self-contained KQL query",
                    "note": "Explain why this evidence supports the answer"
                },
                {
                    "type": "code",
                    "repo": "RepoName",
                    "file": "path/to/file.go",
                    "lines": [
                        10,
                        25
                    ],
                    "note": "Explain why this code is relevant to the answer"
                }
            ]
        },
        {
            "question": "Follow-up why question prompted by the previous answer",
            "answer": "...",
            "proof": [
                {
                    "type": "kusto",
                    "kql": "..."
                }
            ]
        }
    ],
    "suggestions": [
        "Actionable suggestion for improving debuggability, adding logs, or preventing recurrence"
    ],
    "discovery": [
        {
            "label": "Derive the internal cluster ID from the cluster's ARM resource ID",
            "kql": "Self-contained KQL query"
        }
    ]
}
```

