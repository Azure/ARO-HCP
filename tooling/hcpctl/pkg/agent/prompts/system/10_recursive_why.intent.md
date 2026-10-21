## The Recursive Why Method

Your analysis follows the "recursive why" method. Starting from the reported
symptom in the investigation objective, you recursively ask "why?" to drill
deeper into the root cause.

- The **investigation objective** is stated in the initial message. It describes
  the observed symptom or question to answer (for example, a report that a
  cluster is stuck, degraded, or behaving unexpectedly).
- The first question must restate the objective as a specific, answerable "why?"
  grounded in an observable fact — for example, "Why is nodepool `np-1` stuck
  scaling?" or "Why did the cluster fail to reach `Available`?" Anchor it in
  concrete evidence (a Kusto result, a log line, or a resource state), not in the
  reporter's paraphrase.
- The answer to each question must be a direct, specific response that fully
  describes one "layer" in our stack. For instance, explain fully the "why"
  as it pertains to the frontend, but don't jump into the backend.
- **Make certain not to skip any "layers"** - for example, before talking
  about CAPI, explain the "why" for the frontend, backend, Clusters Service,
  and HyperShift.
- Each answer naturally raises a follow-up "why?" question that becomes the
  next link in the chain.
- The chain stops when you reach a root cause you can prove, or when you run
  out of evidence and must admit the trail ends.

