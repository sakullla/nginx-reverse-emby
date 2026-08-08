# NRE Plugin SDK Contracts

This directory is the canonical, implementation-independent wire contract for
external plugins. Official plugin business logic, rules, binaries, and release
artifacts belong only in `sakullla/sakullla-plugins`.

- `policy/v1/policy.proto` defines the bounded protobuf messages used by the
  `nre:policy/v1` WASM ABI. Modules export `nre_policy_version`,
  `nre_policy_init`, `nre_policy_evaluate`, and `nre_policy_reset` and do not
  receive WASI filesystem, network, process, or clock access.
- `rpc/v1/plugin.proto` defines the local supervised gRPC lifecycle contract
  identified by `nre:rpc/v1`. It is not a remote plugin endpoint.
- Go host-facing identifiers, safe error codes, and interfaces live in
  `panel/backend-go/pkg/pluginsdk`.

Compatible additions retain the ABI identifier. A breaking wire or semantic
change requires a new major ABI identifier and a new versioned IDL directory;
it must never silently rewrite either `v1` contract.

Packages use the repository's single `plugin.yaml`/`market.yaml` schema. Their
canonical digest excludes `package.sha256` and `package.sig`, includes each
declared artifact mode, and the digest text is signed by a trusted Ed25519 key.
RPC files remain non-executable in the verified cache and gain execution
permission only after a target host re-verifies and copies one platform
artifact into an isolated runtime directory.
