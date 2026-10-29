English | [简体中文](api.zh-CN.md)

# API reference

The `avr/1` formats (Receipt, attestation response, Receipt Evidence Bundle, Raw Capture) are defined in [docs/avr1.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.md) of the [verirelay/protocol](https://github.com/verirelay/protocol) module.

## Errors

All error responses except those of `/healthz` and `/readyz` (see [Health and readiness](#health-and-readiness)) are JSON of the form:

```json
{"error":{"code":"<code>","message":"<text>","request_id":"<request_id>"}}
```

- `code` is the stable, machine-readable value. Clients should branch on it and on the HTTP status. Each `code` maps to exactly one HTTP status.
- `message` is a human-readable description and may change between versions.
- `request_id` is present on forwarding-route errors other than `unauthorized` and `request_id_failed`, and equals the `X-VeriRelay-Request-Id` response header.
- Every `405 method_not_allowed` response carries an `Allow` header.
- Unknown routes return `404 not_found`.

## Forwarding route

```text
/to/<host>/<upstream-path>
/to/https://<host>/<upstream-path>
```

The Upstream scheme is always `https`; `http://` targets are rejected. VeriRelay forwards the method, query, headers, and body, and returns the Upstream status code and body.

Every request carries `X-VeriRelay-Token: <admin_token>`, matching `admin_token` from the Runtime Config exactly. Without a Runtime Config (development mode only), the forwarding route stays closed.

Upstream validation:

- The host must resolve to public IPs only; `localhost`, `.localhost` and `.local` names are rejected. In development mode, `VERIRELAY_ALLOW_PRIVATE_UPSTREAMS=true` lifts this check.
- Any port is allowed; the default port is 443.
- Redirects are not followed; `3xx` responses are relayed as-is.

VeriRelay error codes:

| HTTP | Code | Cause |
| ---: | --- | --- |
| 400 | `upstream_route_invalid` | Malformed target, or an `http://` scheme. |
| 400 | `request_body_read_failed` | Request body could not be read. |
| 401 | `unauthorized` | `X-VeriRelay-Token` missing or wrong, or `admin_token` not configured. |
| 403 | `upstream_not_allowed` | The target is a local hostname or resolves to a non-public address. |
| 413 | `request_body_too_large` | Request body over 32 MiB. |
| 500 | `request_id_failed`, `request_build_failed` | Internal failures. |
| 502 | `upstream_request_failed` | DNS resolution, connect, or TLS failure. |
| 502 | `upstream_response_too_large` | Non-SSE Upstream response over 64 MiB. |
| 502 | `response_body_read_failed` | Upstream response body read error. |
| 504 | `upstream_timeout` | Network timeout connecting to or reading from the Upstream. There is no overall request timeout. |

## Headers

Forwarded requests drop hop-by-hop and `X-VeriRelay-*` headers, so the token never reaches the Upstream. VeriRelay adds `X-VeriRelay-Request-Id` to them.

Relayed responses drop hop-by-hop and `X-VeriRelay-*` headers, then VeriRelay adds:

| Header | When |
| --- | --- |
| `X-VeriRelay-Request-Id` | Every forward. |
| `X-VeriRelay-Store-Id`, `X-VeriRelay-Store-Key` | The client requested a [Raw Capture](#raw-capture). |

VeriRelay sets no CORS headers and gives `OPTIONS` no special handling.

## Raw Capture

A client may send `X-VeriRelay-Store: <seconds>`, a positive integer, to store the request and response bodies encrypted for that many seconds. A value above the Operator's cap (`raw_store` in [Configuration](configuration.md)) is reduced to the cap.

The response then carries `X-VeriRelay-Store-Id` and `X-VeriRelay-Store-Key`, and the exchange is retrievable at `GET /v1/avr/store/<store_id>` with the out-of-band key.

With an invalid `X-VeriRelay-Store` value, or a capture that cannot be stored, the forward still completes and the response carries no store headers. For an SSE response the store headers are sent before the stream, so a capture of an interrupted stream, or of one over 64 MiB, is not retrievable.

## Receipts

After a complete forward, VeriRelay signs and stores an `avr/1` Receipt keyed by `request_id`, for any Upstream status. A forward is complete once a buffered response is fully written, or once an SSE stream reaches normal EOF.

### `GET /v1/avr/receipts/<request_id>`

| HTTP | Code | Cause |
| ---: | --- | --- |
| 200 | — | Signed `avr/1` Receipt JSON. |
| 400 | `missing_request_id` | The request id is empty or contains `/`. |
| 404 | `receipt_not_found` | Not completed, expired, evicted, or unknown. |
| 405 | `method_not_allowed` | Non-GET method. |
| 500 | `receipt_store_corrupt` | Stored record invalid or signature mismatch. |
| 503 | `receipt_store_unavailable` | Receipt store failure. |

### `GET /v1/avr/evidence/<request_id>`

Returns a Receipt Evidence Bundle: `{"version":"avr/1","receipt":{...},"attestation":{...}|null}`, specified in section "Receipt Evidence Bundle" of [docs/avr1.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.md).

`attestation` is the attestation response archived at startup (all-zero nonce), which binds the Receipt Signing Key to the Attestation Report. It is `null` when the archive has expired or is missing.

Same status table as the Receipt endpoint, plus:

| HTTP | Code | Cause |
| ---: | --- | --- |
| 500 | `attestation_store_corrupt` | Corrupt archived attestation record. |
| 503 | `attestation_store_unavailable` | Attestation store failure. |

### `GET /v1/avr/store/<store_id>`

Returns the Raw Capture envelope for a captured exchange, specified in section "Raw Capture" of [docs/avr1.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.md).

| HTTP | Code | Cause |
| ---: | --- | --- |
| 200 | — | Raw Capture envelope JSON. |
| 400 | `missing_store_id` | The store id is empty or contains `/`. |
| 404 | `store_not_found` | Expired, evicted, unknown, or Raw Capture disabled in development mode. |
| 405 | `method_not_allowed` | Non-GET method. |
| 500 | `store_corrupt` | Stored envelope invalid. |
| 503 | `store_unavailable` | Store failure. |

### `GET /v1/avr/attestation?nonce=<nonce>`

`nonce` is the Attestation Nonce: 32 bytes, unpadded base64url. Returns a fresh attestation response binding the Receipt Signing Key to the nonce, specified in section "Attestation response" of [docs/avr1.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.md).

| HTTP | Code | Cause |
| ---: | --- | --- |
| 400 | `missing_nonce`, `invalid_nonce` | Missing or malformed nonce. |
| 405 | `method_not_allowed` | Non-GET method. |
| 500 | `receipt_public_key_invalid` | Internal key failure. |
| 500 | `attestation_disclosure_failed` | Guest Manifest missing, invalid, or unreadable. |
| 500 | `attestation_report_unsupported` | The report is VLEK-signed; only VCEK is supported. |
| 502 | `attestation_report_failed` | Attestation Report collection or AMD KDS failure. |
| 503 | `attestation_unavailable` | Attestation not configured or no SEV-SNP device available. |
| 503 | `attestation_busy` | Too many attestation requests waiting; retry after `Retry-After`. |

### Attestation disclosure fields

`source.verirelay.{repository,commit}`, `source.veriforge.{repository,commit}`, and `launch_profile` come verbatim from the Guest Manifest `/etc/verirelay/manifest.json` (`verirelay-guest-manifest/1`). What they mean is defined in the Guest Manifest section of [docs/guest.md](https://github.com/verirelay/protocol/blob/main/docs/guest.md) in the [verirelay/protocol](https://github.com/verirelay/protocol) module.

`bootstrap` is the base64url of the exact Bootstrap bytes VeriRelay read from the Config Disk. Its SHA-256 equals the report's `HOST_DATA`. It is absent in development mode.

## Health and readiness

- `GET /healthz` returns `200 ok` whenever the process can respond.
- `GET /readyz` returns `200 ok` once startup has completed and the Receipt and attestation stores pass their readiness checks; otherwise `503 not_ready`.

Both bodies are plain text. Until startup has completed, every other route returns the JSON error `503 not_ready`.

Store readiness only affects `/readyz`: during a store outage the other routes keep serving and report their own store errors, for example `503 receipt_store_unavailable`. A forward whose Receipt cannot be stored is still relayed, but that Receipt is not retrievable.

## SSE semantics

`text/event-stream` responses are relayed byte for byte as they arrive. `response_body_sha256` covers exactly the bytes written to the client.
