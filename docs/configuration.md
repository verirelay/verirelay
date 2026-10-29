English | [简体中文](configuration.zh-CN.md)

# Configuration

## Production and development mode

VeriRelay runs in production mode unless `VERIRELAY_MODE=development` is set exactly. In a CVM, the environment of the init process comes from the measured kernel command line, so a Launch Measurement without `VERIRELAY_MODE=development` always runs in production mode.

| | Production | Development |
| --- | --- | --- |
| Listen address | `:8080` | `VERIRELAY_LISTEN_ADDR` |
| Configuration source | The Runtime Config from VeriProv | Environment variables and `.env`, optionally a Runtime Config |
| Other `VERIRELAY_*` variables and `.env` | Ignored | Read |
| Bootstrap and `HOST_DATA` check | Required | None |

## Production startup

1. Read the Bootstrap from the Config Disk and check it against `HOST_DATA`.
2. Run `veriprov/1` Provisioning against the VeriProv the Bootstrap names.
3. Create the Receipt Signing Key and the stores, archive the startup attestation response, and start serving.

Every failure is logged and retried until startup succeeds. [`/readyz`](api.md#health-and-readiness) reports when it has.

## Bootstrap and Config Disk

The host delivers the Bootstrap on the Config Disk, a read-only raw virtio block device, and binds it to the Attestation Report through `HOST_DATA`.

The Bootstrap format, the disk layout and the binding are specified in [Guest files](https://github.com/verirelay/protocol/blob/main/docs/guest.md), section "Bootstrap". VeriRelay applies them as follows:

- The CVM must have exactly one virtio block device (`/dev/vd*`).
- VeriRelay reaches VeriProv at the Bootstrap `url` over TLS, validated against the CA bundle in the initrd, and follows no redirects.
- VeriRelay accepts only a provision response envelope signed by the Bootstrap `identity_public_key`.

## Runtime Config from VeriProv

The Runtime Config format, defaults and Redis key layout are specified in [`veriprov/1`](https://github.com/verirelay/protocol/blob/main/docs/veriprov1.md), section "Runtime Config".

| Field | Effect |
| --- | --- |
| `admin_token` | The token clients send in `X-VeriRelay-Token` to use the forwarding route `/to/`. |
| `receipt_store` | Where Receipts and the archived attestation response are kept; process memory by default. |
| `raw_store` | Where Raw Captures are kept. |

Without `raw_store`, Raw Captures share a Redis `receipt_store`, or else stay in process memory. Production cannot disable Raw Capture.

The store fields act as follows:

| Field | Effect |
| --- | --- |
| `type` | `memory` keeps records in process memory; `redis` keeps them in Redis or Valkey. |
| `addr`, `tls`, `username`, `password`, `db` | The Redis connection. |
| `key_prefix` | The prefix of every Redis key. |
| `receipt_store.ttl_seconds` | The Receipt TTL, for both store types. |
| `receipt_store.max_entries` | Most Receipts the memory store keeps. |
| `raw_store.max_ttl_seconds` | Longest retention a client may request; `0` or absent means no cap. |
| `raw_store.max_bytes` | Most bytes of Raw Captures the memory store keeps. |

## SEV-SNP endorsements

VeriRelay fetches the VCEK, ASK and ARK from the AMD KDS and returns them in `endorsements`, so the CVM needs outbound HTTPS access to the AMD KDS from startup on. Only VCEK-signed Attestation Reports are supported.

## Guest files

| Path | Content |
| --- | --- |
| `/etc/verirelay/manifest.json` | The Guest Manifest (`verirelay-guest-manifest/1`), disclosed as `source` and `launch_profile`. |
| `/dev/vd*` | The Config Disk, which holds the Bootstrap (production only). |
| `/etc/ssl/certs/ca-certificates.crt` | The CA bundle for outbound TLS: VeriProv, Upstreams, the AMD KDS and Redis. |

The Guest Manifest is required: without a valid manifest, startup and the attestation endpoint fail.

## Development environment variables

Development mode loads a `.env` file from the working directory; variables already set in the environment win. `VERIRELAY_MODE` itself is read only from the environment.

### Core

| Variable | Default | Description |
| --- | --- | --- |
| `VERIRELAY_MODE` | empty | `development` selects development mode; any other value is production. |
| `VERIRELAY_LISTEN_ADDR` | `:8080` | HTTP listen address. |
| `VERIRELAY_DEBUG_ERRORS` | `false` | Append the underlying error to the `message` of error responses. |
| `VERIRELAY_ALLOW_PRIVATE_UPSTREAMS` | `false` | Allow localhost, private, link-local, and metadata IP Upstreams. |

### Attestation

| Variable | Default | Description |
| --- | --- | --- |
| `VERIRELAY_FAKE_ATTESTATION` | `false` | Use placeholder Attestation Reports and endorsements. |
| `VERIRELAY_RECEIPT_KEY_SEED` | empty | Ed25519 seed for the `avr/1` Receipt Signing Key. Requires `VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED=true`. |
| `VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED` | `false` | Explicitly allow loading `VERIRELAY_RECEIPT_KEY_SEED`. |

### Provisioning

A local Runtime Config takes precedence over VeriProv. With neither, development mode runs without a Runtime Config: the stores come from the variables below and the forwarding route is closed.

| Variable | Default | Description |
| --- | --- | --- |
| `VERIRELAY_VERIPROV_URL` | empty | VeriProv HTTPS origin; setting it enables Provisioning at startup. |
| `VERIRELAY_VERIPROV_IDENTITY_PUBLIC_KEY` | empty | Public key of the VeriProv Identity Key, unpadded base64url of 32 bytes. Required with `VERIRELAY_VERIPROV_URL`. |
| `VERIRELAY_PROVISIONING_ENABLED` | `true` when the URL is set | Explicitly enable or disable Provisioning. Enabled requires the URL. |
| `VERIRELAY_PROVISIONING_TIMEOUT` | `30s` | Timeout of one Provisioning attempt; positive duration. |
| `VERIRELAY_RUNTIME_CONFIG` | empty | Inline Runtime Config JSON. Requires `VERIRELAY_ALLOW_TEST_CONFIG=true`. |
| `VERIRELAY_RUNTIME_CONFIG_FILE` | empty | Runtime Config file path. Mutually exclusive with the inline source; requires `VERIRELAY_ALLOW_TEST_CONFIG=true`. |
| `VERIRELAY_ALLOW_TEST_CONFIG` | `false` | Allow a local Runtime Config source. |
| `VERIRELAY_FAKE_PROVISIONING_QUOTE` | `false` | Send a fake Attestation Report to VeriProv. Requires `VERIRELAY_ALLOW_TEST_CONFIG=true`; a real VeriProv rejects it. |

### Receipt store

| Variable | Default | Description |
| --- | --- | --- |
| `VERIRELAY_RECEIPT_STORE_TYPE` | `memory` | `memory`, `redis`, or `valkey` (the same as `redis`). |
| `VERIRELAY_RECEIPT_STORE_TTL` | `1h` | Receipt TTL; positive duration. |
| `VERIRELAY_RECEIPT_STORE_MAX_ENTRIES` | `10000` | Most Receipts the `memory` store keeps; positive integer. |
| `VERIRELAY_RECEIPT_STORE_ADDR` | empty | Redis/Valkey address; required for `redis`/`valkey`. |
| `VERIRELAY_RECEIPT_STORE_TLS` | `false` | Enable TLS to Redis/Valkey. |
| `VERIRELAY_RECEIPT_STORE_USERNAME` | empty | Redis ACL username. |
| `VERIRELAY_RECEIPT_STORE_PASSWORD_FILE` | empty | Read the Redis/Valkey password from a file. |
| `VERIRELAY_RECEIPT_STORE_DB` | `0` | Redis DB; non-negative integer. |
| `VERIRELAY_RECEIPT_STORE_CONNECT_TIMEOUT` | `5s` | Positive duration. |
| `VERIRELAY_RECEIPT_STORE_READ_TIMEOUT` | `2s` | Positive duration. |
| `VERIRELAY_RECEIPT_STORE_WRITE_TIMEOUT` | `2s` | Positive duration. |
| `VERIRELAY_RECEIPT_STORE_KEY_PREFIX` | `verirelay:` | Non-empty key prefix. |

### Raw Capture store

When `VERIRELAY_RAW_STORE_TYPE` is unset, Raw Captures share a Redis Receipt store, or else use a memory store.

| Variable | Default | Description |
| --- | --- | --- |
| `VERIRELAY_RAW_STORE_TYPE` | empty | `memory`, `redis`, `valkey` (the same as `redis`), or `disabled`. |
| `VERIRELAY_RAW_STORE_MAX_BYTES` | `268435456` | Most bytes the `memory` store keeps; positive integer. |
| `VERIRELAY_RAW_STORE_MAX_TTL` | empty | Positive duration capping the retention a client may request; empty means no cap. |
| `VERIRELAY_RAW_STORE_ADDR` | empty | Redis/Valkey address; required for `redis`/`valkey`. |
| `VERIRELAY_RAW_STORE_TLS` | `false` | Enable TLS to Redis/Valkey. |
| `VERIRELAY_RAW_STORE_USERNAME` | empty | Redis ACL username. |
| `VERIRELAY_RAW_STORE_PASSWORD_FILE` | empty | Read the Redis/Valkey password from a file. |
| `VERIRELAY_RAW_STORE_DB` | `0` | Redis DB; non-negative integer. |
| `VERIRELAY_RAW_STORE_CONNECT_TIMEOUT` | `5s` | Positive duration. |
| `VERIRELAY_RAW_STORE_READ_TIMEOUT` | `2s` | Positive duration. |
| `VERIRELAY_RAW_STORE_WRITE_TIMEOUT` | `2s` | Positive duration. |
| `VERIRELAY_RAW_STORE_KEY_PREFIX` | `verirelay:` | Non-empty key prefix. |
