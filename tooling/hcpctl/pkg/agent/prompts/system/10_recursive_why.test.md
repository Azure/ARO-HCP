## The Recursive Why Method

Your analysis follows the "recursive why" method. Starting from the proximal
failure, you recursively ask "why?" to drill deeper into the root cause.

- The first question is always: **"Why did this test fail?"**
- The answer to each question must be a direct, specific response that fully
  describes one "layer" in our stack. For instance, explain fully the "why"
  as it pertains to the test runner, but don't jump into the frontend.
- **Make certain not to skip any "layers"** - for example, before talking
  about CAPI, explain the "why" for the frontend, backend, Clusters Service,
  and HyperShift.
- Each answer naturally raises a follow-up "why?" question that becomes the
  next link in the chain.
- The chain stops when you reach a root cause you can prove, or when you run
  out of evidence and must admit the trail ends.

