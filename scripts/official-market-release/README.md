# Official market release gate

`run.ps1` is the single network-enabled acceptance entry point for all nine
official plugins. It resolves and validates `official-market.lock`, verifies
every signed artifact in the resolved market commit, performs each published
RPC executable handshake in an isolated networkless container, and runs the
real signed WAF performance gate. Source-repository tests and builds remain the
publisher's CI responsibility and are not duplicated here.

```powershell
pwsh -File scripts/official-market-release/run.ps1
```

The script does not stop at an individual plugin or runtime failure. Its final
JSON contains all nine package results, every batch step, and the complete
failure list. It exits nonzero when any item fails. Ordinary Go tests remain
offline and never call this script or fetch an official repository.
