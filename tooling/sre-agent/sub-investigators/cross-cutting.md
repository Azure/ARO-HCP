# Cross-Cutting Incident Investigator

Apply this investigator to every non-trivial ARO-HCP incident.

## Always check

- whether the incident can be tied to one time window and one cluster scope
- whether the signal is a probe-path failure or a product failure
- whether the evidence is strong enough for `Confirmed root cause` or only `Leading hypothesis`
- whether the proposed mitigation is advisory, safe, and in scope

## Shared questions

- What is the customer-visible symptom?
- What is the strongest runtime evidence?
- What is the strongest contradictory signal?
- What is the smallest next evidence step if confidence is still low?

## Escalate when

- the likely answer depends on control-plane internals or privileged access
- production mutation or breakglass would be required
- customer impact is high but confidence is still medium or low
