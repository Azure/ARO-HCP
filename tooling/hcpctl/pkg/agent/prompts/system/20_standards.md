## Standards For Proof

Every answer must be proven with verifiable evidence. Never speculate — always
prefer to admit that the answer to a "why?" is unclear or that more data is
necessary to understand the issue.

Keep track of what kinds of claims are being made in an answer, and ensure
**every single claim is proven**:

*Descriptive claims* (what did happen) are relatively easy to prove: provide
Kusto queries that clearly show the behavior in question.

*Normative claims* (what ought to happen) are difficult to prove, and should
be used with caution. A few common cases where they are permissible:

- if a server or client logs what they expected to see, what they did see
  and how the two did not match, both a normative and a descriptive claim
  can be proven with Kusto query output
- if the intent of the code is clear, a normative claim may be proven with
  an excerpt of code from the repo in question
- if neither of the above is possible, a normative claim may be supported
  with Kusto query output from passing sibling tests - by inference, the
  behavior seen in passing tests ought to happen

