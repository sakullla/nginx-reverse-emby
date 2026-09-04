#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
compose_file="$script_dir/acme-integration/docker-compose.yaml"

for expected_image in \
    'ghcr.io/letsencrypt/pebble:2.10.1@sha256:ddf230642b1a584f519f32e347de1b05a6e4c1f6c35c1863b33effeab5f78199' \
    'ghcr.io/letsencrypt/pebble-challtestsrv:2.10.1@sha256:12ce21884def456bcf9786542113949e1f19dc7738d2c70e156c2d0c38a1405b' \
    'curlimages/curl:8.12.1@sha256:94e9e444bcba979c2ea12e27ae39bee4cd10bc7041a472c4727a558e213744e6'
do
    if ! grep -Fq "image: $expected_image" "$compose_file"; then
        printf 'ACME fixture image is not pinned to %s\n' "$expected_image" >&2
        exit 1
    fi
done

for expected_port in \
    '127.0.0.1:${NRE_ACME_TEST_DIRECTORY_PORT:-14000}:14000' \
    '127.0.0.1:${NRE_ACME_TEST_MANAGEMENT_PORT:-15000}:15000' \
    '127.0.0.1:${NRE_ACME_TEST_CHALLTESTSRV_PORT:-8055}:8055'
do
    if ! grep -Fq "$expected_port" "$compose_file"; then
        printf 'ACME fixture port is not loopback-bound: %s\n' "$expected_port" >&2
        exit 1
    fi
done

read_only_count="$(grep -Fc 'read_only: true' "$compose_file")"
no_new_privileges_count="$(grep -Fc 'no-new-privileges:true' "$compose_file")"
if [ "$read_only_count" -ne 3 ] || [ "$no_new_privileges_count" -ne 3 ]; then
    printf 'ACME fixture services must all be read-only and no-new-privileges\n' >&2
    exit 1
fi

printf 'ACME integration fixture security tests passed\n'
