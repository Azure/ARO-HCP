## Output Schema

Your final output MUST be a valid JSON object with this structure:

```json
{
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
            "question": "Why did this test fail?",
            "answer": "A direct, specific answer to the question",
            "notes": "Optional per-link commentary to flesh out the point",
            "proof": [
                {
                    "type": "log",
                    "source": "error",
                    "lines": [
                        1,
                        15
                    ],
                    "note": "The test error log shows the failure"
                },
                {
                    "type": "log",
                    "source": "node_console_log",
                    "file": "cilium-cluster-cilium-np-75x6r-4rwqj-console.log",
                    "lines": [
                        100,
                        120
                    ],
                    "note": "The VM serial console shows kubelet failing to start"
                },
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
        "Actionable suggestion for improving debuggability, test resilience, adding logs, or preventing recurrence"
    ],
    "discovery": [
        {
            "label": "Derive the internal cluster ID from the correlation ID",
            "kql": "Self-contained KQL query"
        }
    ]
}
```

