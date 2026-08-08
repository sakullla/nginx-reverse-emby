# NRE Plugin SDK Contracts

This directory is the canonical, implementation-independent wire contract for
external plugins. Official plugin business logic, rules, binaries, and release
artifacts belong only in `sakullla/sakullla-plugins`.

- `policy/v1/policy.proto` defines the bounded protobuf messages and complete
  `nre:policy/v1` WASM calling convention: guest allocator/free ownership,
  pointer-length frames, numeric status encoding, required exports, and the
  only permitted Host imports. Modules do not receive WASI filesystem,
  network, process, clock, imported memory, or undeclared Host access.
- `policy/v1/testdata/compatible_guest.wasm.hex` is a real WebAssembly 1.0
  compatibility fixture (hex encoded for portable source review). Backend
  tests decode it and run the same structural and ABI validator used for
  signed packages; the header-only eight-byte module is intentionally invalid.
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
