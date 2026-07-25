# Local ACME Integration Fixture

This fixture runs Pebble and `pebble-challtestsrv` for the opt-in Go integration tier. It is test-only, keeps all CA and challenge state in memory, and does not read production tokens, certificates, or `panel/data`.

Start it from the repository root:

```sh
docker compose -f scripts/acme-integration/docker-compose.yaml up -d --wait --wait-timeout 60
```

The default endpoints are:

- ACME directory: `https://127.0.0.1:14000/dir`
- Pebble management: `https://127.0.0.1:15000`
- Challenge management: `http://127.0.0.1:8055`

Pebble's HTTPS certificate uses its public test CA. Extract that CA to a temporary path and pass the fixture contract to tests:

```sh
pebble_ca="${TMPDIR:-/tmp}/nre-pebble.minica.pem"
docker compose -f scripts/acme-integration/docker-compose.yaml cp pebble:/test/certs/pebble.minica.pem "$pebble_ca"
export SSL_CERT_FILE="$pebble_ca"
export NRE_ACME_TEST_DIRECTORY_URL=https://127.0.0.1:14000/dir
export NRE_ACME_TEST_MANAGEMENT_URL=https://127.0.0.1:15000
export NRE_ACME_TEST_CHALLTESTSRV_URL=http://127.0.0.1:8055
```

The directory advertises `default` and `shortlived` profiles. Challenge validation is enabled; only Pebble's random validation delay, nonce rejection, and authorization reuse are disabled for deterministic runs.

If a default host port is occupied, set `NRE_ACME_TEST_DIRECTORY_PORT`, `NRE_ACME_TEST_MANAGEMENT_PORT`, or `NRE_ACME_TEST_CHALLTESTSRV_PORT` before `docker compose up`, then use the matching URL in the test environment. `NRE_ACME_TEST_BIND_ADDRESS` changes the host bind address and defaults to `127.0.0.1`.

Stop and remove the ephemeral fixture after the full tier:

```sh
docker compose -f scripts/acme-integration/docker-compose.yaml down --volumes --remove-orphans
```
