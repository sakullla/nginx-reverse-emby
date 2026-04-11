Place copied production-like `panel/data` fixtures here when running cutover verification locally.

Recommended contents:
- `panel.db` copied from a non-production-safe snapshot
- `managed_certificates/` material directories needed by the copied data
- any local-agent state required to reproduce startup behavior

Do not commit real tokens, private keys, or live production data.
