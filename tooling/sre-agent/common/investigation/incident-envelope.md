# Incident Envelope

Normalize the incoming incident before routing.

## Required fields

- Intake mode: alert, dashboard symptom, raw logs, or customer statement
- Primary symptom: short statement of the failure
- Time window: precise when known, otherwise `Not yet determined`
- Cluster scope: cluster name, HCP namespace, or `Not yet determined`
- Join keys: `probe_url`, namespace, cluster name, or `Not yet determined`

Keep the envelope stable across the domain memo and final TSG.
