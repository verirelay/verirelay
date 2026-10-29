[English](api.md) | 简体中文

# API 参考

`avr/1` 格式（收据、证明接口响应、收据证据包、原文留存）定义在 [verirelay/protocol](https://github.com/verirelay/protocol) 模块的 [docs/avr1.zh-CN.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.zh-CN.md)。

## 错误

除 `/healthz` 和 `/readyz`（见 [Health 与 Readiness](#health-与-readiness)）外，所有错误响应都是如下格式的 JSON：

```json
{"error":{"code":"<code>","message":"<text>","request_id":"<request_id>"}}
```

- `code` 是稳定的机器判据。客户端按它和 HTTP 状态码分支。每个 `code` 只对应一个 HTTP 状态码。
- `message` 是给人看的说明，不同版本之间可能变化。
- `request_id` 出现在除 `unauthorized` 和 `request_id_failed` 外的转发路由错误里，与响应 header `X-VeriRelay-Request-Id` 相同。
- 所有 `405 method_not_allowed` 响应都带 `Allow` header。
- 未知路由返回 `404 not_found`。

## 转发路由

```text
/to/<host>/<upstream-path>
/to/https://<host>/<upstream-path>
```

上游 scheme 固定为 `https`，`http://` 目标会被拒绝。VeriRelay 转发 method、query、header 和 body，并返回上游的状态码与 body。

每个请求都携带 `X-VeriRelay-Token: <admin_token>`，值与运行配置中的 `admin_token` 完全一致。没有运行配置时（仅开发模式），转发路由保持关闭。

上游校验：

- host 只能解析到公网 IP，`localhost`、`.localhost` 和 `.local` 名称会被拒绝。开发模式下设置 `VERIRELAY_ALLOW_PRIVATE_UPSTREAMS=true` 可取消这项检查。
- 允许任意端口，默认端口 443。
- 不跟随重定向，`3xx` 响应原样转发。

VeriRelay 错误码：

| HTTP | Code | 原因 |
| ---: | --- | --- |
| 400 | `upstream_route_invalid` | 目标格式错误，或使用 `http://` scheme |
| 400 | `request_body_read_failed` | 请求 body 读取失败 |
| 401 | `unauthorized` | `X-VeriRelay-Token` 缺失、不匹配，或未配置 `admin_token` |
| 403 | `upstream_not_allowed` | 目标是本地主机名，或解析到非公网地址 |
| 413 | `request_body_too_large` | 请求 body 超过 32 MiB |
| 500 | `request_id_failed`, `request_build_failed` | 内部错误 |
| 502 | `upstream_request_failed` | DNS 解析、连接或 TLS 失败 |
| 502 | `upstream_response_too_large` | 非 SSE 的上游响应超过 64 MiB |
| 502 | `response_body_read_failed` | 上游响应 body 读取失败 |
| 504 | `upstream_timeout` | 连接上游或读取上游响应时发生网络超时；请求没有整体超时 |

## Header

转发请求会移除 hop-by-hop header 与 `X-VeriRelay-*`，因此 token 不会到达上游。VeriRelay 给转发请求加上 `X-VeriRelay-Request-Id`。

透传的响应会移除 hop-by-hop header 和 `X-VeriRelay-*`，再由 VeriRelay 加上：

| Header | 何时出现 |
| --- | --- |
| `X-VeriRelay-Request-Id` | 每次转发 |
| `X-VeriRelay-Store-Id`, `X-VeriRelay-Store-Key` | 客户端请求了[原文留存](#原文留存) |

VeriRelay 不设置 CORS header，也不特殊处理 `OPTIONS`。

## 原文留存

客户端可发送 `X-VeriRelay-Store: <seconds>`（正整数），将请求和响应 body 加密存储相应的秒数。超过运营者设置的上限（见 [配置](configuration.zh-CN.md) 中的 `raw_store`）时按上限保存。

响应携带 `X-VeriRelay-Store-Id` 与 `X-VeriRelay-Store-Key`，凭带外获取的 key 可在 `GET /v1/avr/store/<store_id>` 取回。

`X-VeriRelay-Store` 非法，或者捕获无法保存时，转发照常完成，只是响应不带 store header。SSE 响应的 store header 在流开始前发出，因此中断或超过 64 MiB 的流无法取回。

## 收据

完整转发后，无论上游状态码是什么，VeriRelay 都以 `request_id` 为键签名并存储 `avr/1` 收据。缓冲响应完整写出，或 SSE 流正常 EOF，才算完整转发。

### `GET /v1/avr/receipts/<request_id>`

| HTTP | Code | 原因 |
| ---: | --- | --- |
| 200 | — | 已签名的 `avr/1` 收据 JSON |
| 400 | `missing_request_id` | request id 为空或包含 `/` |
| 404 | `receipt_not_found` | 未完成、过期、被淘汰或不存在 |
| 405 | `method_not_allowed` | 非 GET 方法 |
| 500 | `receipt_store_corrupt` | 存储记录非法或签名不符 |
| 503 | `receipt_store_unavailable` | 收据 store 故障 |

### `GET /v1/avr/evidence/<request_id>`

返回收据证据包：`{"version":"avr/1", "receipt":{...}, "attestation":{...}|null}`，格式见 [docs/avr1.zh-CN.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.zh-CN.md) 的「收据证据包」一节。

`attestation` 是启动时归档的证明接口响应（全零 nonce），它把收据签名密钥绑定到证明报告。归档过期或缺失时为 `null`。

状态码与收据接口一致，另加：

| HTTP | Code | 原因 |
| ---: | --- | --- |
| 500 | `attestation_store_corrupt` | 归档的证明记录损坏 |
| 503 | `attestation_store_unavailable` | 远程证明 store 故障 |

### `GET /v1/avr/store/<store_id>`

返回被捕获报文的原文留存 envelope，格式见 [docs/avr1.zh-CN.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.zh-CN.md) 的「原文留存」一节。

| HTTP | Code | 原因 |
| ---: | --- | --- |
| 200 | — | 原文留存 envelope JSON |
| 400 | `missing_store_id` | store id 为空或包含 `/` |
| 404 | `store_not_found` | 已过期、被淘汰、不存在，或在开发模式禁用原文留存 |
| 405 | `method_not_allowed` | 非 GET 方法 |
| 500 | `store_corrupt` | 存储的 envelope 非法 |
| 503 | `store_unavailable` | store 故障 |

### `GET /v1/avr/attestation?nonce=<nonce>`

`nonce` 为 Attestation Nonce：32 字节，无 padding 的 base64url。返回一份将收据签名密钥与 nonce 绑定的新证明接口响应，格式见 [docs/avr1.zh-CN.md](https://github.com/verirelay/protocol/blob/main/docs/avr1.zh-CN.md) 的「证明响应」一节。

| HTTP | Code | 原因 |
| ---: | --- | --- |
| 400 | `missing_nonce`, `invalid_nonce` | nonce 缺失或格式非法 |
| 405 | `method_not_allowed` | 非 GET 方法 |
| 500 | `receipt_public_key_invalid` | 内部 key 错误 |
| 500 | `attestation_disclosure_failed` | 来宾启动声明缺失、非法或读取失败 |
| 500 | `attestation_report_unsupported` | 证明报告由 VLEK 签名，只支持 VCEK |
| 502 | `attestation_report_failed` | 证明报告采集或 AMD KDS 失败 |
| 503 | `attestation_unavailable` | 远程证明未配置，或没有可用的 SEV-SNP 设备 |
| 503 | `attestation_busy` | 等待中的证明请求过多，按 `Retry-After` 稍后重试 |

### 远程证明披露字段

`source.verirelay.{repository, commit}`、`source.veriforge.{repository, commit}` 与 `launch_profile` 原样来自来宾启动声明 `/etc/verirelay/manifest.json`（`verirelay-guest-manifest/1`）。其含义定义在 [verirelay/protocol](https://github.com/verirelay/protocol) 模块 [docs/guest.zh-CN.md](https://github.com/verirelay/protocol/blob/main/docs/guest.zh-CN.md) 的「来宾启动声明（Guest Manifest）」一节。

`bootstrap` 是 VeriRelay 从配置盘读到的引导文件原始字节的 base64url。其 SHA-256 等于证明报告的 `HOST_DATA`。开发模式下不出现。

## Health 与 Readiness

- `GET /healthz`：进程可响应即返回 `200 ok`。
- `GET /readyz`：启动完成且收据 store 与远程证明 store 通过 readiness 检查后返回 `200 ok`，否则 `503 not_ready`。

两者都是纯文本 body。启动完成前，其余所有路由返回 JSON 错误 `503 not_ready`。

store 的 readiness 只影响 `/readyz`：store 故障期间，其余路由继续服务并报告各自的 store 错误（例如 `503 receipt_store_unavailable`）。收据无法存储的转发请求仍会被转发，但该收据无法取回。

## SSE 语义

`text/event-stream` 响应在到达时逐字节透传。`response_body_sha256` 只覆盖实际写给客户端的字节。
