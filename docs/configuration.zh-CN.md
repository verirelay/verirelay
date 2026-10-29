[English](configuration.md) | 简体中文

# 配置

## 生产模式与开发模式

除非精确设置 `VERIRELAY_MODE=development`，VeriRelay 都以生产模式运行。在 CVM 中，init 进程的环境变量来自参与度量的内核命令行，因此不含 `VERIRELAY_MODE=development` 的启动度量总是以生产模式运行。

| | 生产模式 | 开发模式 |
| --- | --- | --- |
| 监听地址 | `:8080` | `VERIRELAY_LISTEN_ADDR` |
| 配置来源 | VeriProv 下发的运行配置 | 环境变量和 `.env`，可选一份运行配置 |
| 其他 `VERIRELAY_*` 变量和 `.env` | 忽略 | 读取 |
| 引导文件和 `HOST_DATA` 校验 | 必需 | 无 |

## 生产模式启动

1. 从配置盘读取引导文件，并与 `HOST_DATA` 核对。
2. 向引导文件指定的 VeriProv 执行 `veriprov/1` 配置下发。
3. 生成收据签名密钥和各 store，归档启动时的证明接口响应，开始服务。

任何失败都会记录日志并一直重试，直到启动成功。可用 [`/readyz`](api.zh-CN.md#health-与-readiness) 判断启动是否完成。

## 引导文件与配置盘

宿主机把引导文件放在配置盘（一个只读的 raw virtio 块设备）上交给 CVM，并通过 `HOST_DATA` 把它绑定到证明报告。

引导文件格式、配置盘布局和绑定方式定义在 [Guest 文件](https://github.com/verirelay/protocol/blob/main/docs/guest.zh-CN.md) 的「引导文件（Bootstrap）」一节。VeriRelay 按如下方式使用：

- CVM 中必须恰好有一个 virtio 块设备（`/dev/vd*`）。
- VeriRelay 通过 TLS 访问引导文件 `url` 指定的 VeriProv，用 initrd 中的 CA bundle 校验证书，不跟随重定向。
- VeriRelay 只接受由引导文件的 `identity_public_key` 签名的 provision 响应 envelope。

## VeriProv 下发的运行配置

运行配置的格式、默认值和 Redis key 布局定义在 [`veriprov/1`](https://github.com/verirelay/protocol/blob/main/docs/veriprov1.zh-CN.md) 的「运行配置」一节。

| 字段 | 作用 |
| --- | --- |
| `admin_token` | 客户端在 `X-VeriRelay-Token` 中发送的 token，用于转发路由 `/to/` |
| `receipt_store` | 收据和归档的证明接口响应存放的位置，默认在进程内存中 |
| `raw_store` | 原文留存存放的位置 |

省略 `raw_store` 时，`receipt_store` 为 Redis 则与其共用，否则在进程内存中。生产模式无法关闭原文留存。

store 字段作用如下：

| 字段 | 作用 |
| --- | --- |
| `type` | `memory` 把记录保存在进程内存中；`redis` 保存在 Redis 或 Valkey 中 |
| `addr`, `tls`, `username`, `password`, `db` | Redis 连接 |
| `key_prefix` | 所有 Redis key 的前缀 |
| `receipt_store.ttl_seconds` | 收据 TTL，两种 store 类型都适用 |
| `receipt_store.max_entries` | memory store 最多保留的收据数 |
| `raw_store.max_ttl_seconds` | 客户端可请求的最长保存时长，`0` 或省略表示不限 |
| `raw_store.max_bytes` | memory store 最多保留的原文留存字节数 |

## SEV-SNP endorsements

VeriRelay 从 AMD KDS 获取 VCEK、ASK 和 ARK，放进 `endorsements` 返回，因此 CVM 从启动起就需要能通过 HTTPS 访问 AMD KDS。只支持 VCEK 签名的证明报告。

## Guest 文件

| 路径 | 内容 |
| --- | --- |
| `/etc/verirelay/manifest.json` | 来宾启动声明（`verirelay-guest-manifest/1`），以 `source` 和 `launch_profile` 公开 |
| `/dev/vd*` | 配置盘，存放引导文件（仅生产模式） |
| `/etc/ssl/certs/ca-certificates.crt` | 对外 TLS 连接使用的 CA bundle：VeriProv、上游、AMD KDS 和 Redis |

来宾启动声明是必需的：没有合法的来宾启动声明，启动和证明接口都会失败。

## 开发模式环境变量

开发模式会加载工作目录中的 `.env`，环境中已设置的变量优先。`VERIRELAY_MODE` 本身只从环境读取。

### 核心

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VERIRELAY_MODE` | 空 | `development` 进入开发模式，其他任何值都是生产模式 |
| `VERIRELAY_LISTEN_ADDR` | `:8080` | HTTP listen 地址 |
| `VERIRELAY_DEBUG_ERRORS` | `false` | 在错误响应的 `message` 后附上底层错误原文 |
| `VERIRELAY_ALLOW_PRIVATE_UPSTREAMS` | `false` | 允许 localhost、私网、link-local、metadata IP 等上游 |

### 远程证明

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VERIRELAY_FAKE_ATTESTATION` | `false` | 使用占位证明报告和 endorsements |
| `VERIRELAY_RECEIPT_KEY_SEED` | 空 | `avr/1` 收据签名密钥的 Ed25519 seed，需要 `VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED=true` |
| `VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED` | `false` | 显式允许加载 `VERIRELAY_RECEIPT_KEY_SEED` |

### 配置下发

本地运行配置优先于 VeriProv。两者都没有时，开发模式不使用运行配置：store 来自下面的变量，转发路由关闭。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VERIRELAY_VERIPROV_URL` | 空 | VeriProv HTTPS origin；设置后在启动时执行配置下发 |
| `VERIRELAY_VERIPROV_IDENTITY_PUBLIC_KEY` | 空 | VeriProv 身份密钥的公钥，32 字节，无 padding 的 base64url；设置 `VERIRELAY_VERIPROV_URL` 时必填 |
| `VERIRELAY_PROVISIONING_ENABLED` | URL 非空时 `true` | 显式启用/禁用配置下发；启用时必须设置 URL |
| `VERIRELAY_PROVISIONING_TIMEOUT` | `30s` | 一次配置下发尝试的超时，正 duration |
| `VERIRELAY_RUNTIME_CONFIG` | 空 | inline 运行配置 JSON，需要 `VERIRELAY_ALLOW_TEST_CONFIG=true` |
| `VERIRELAY_RUNTIME_CONFIG_FILE` | 空 | 运行配置文件路径；与 inline source 互斥，需要 `VERIRELAY_ALLOW_TEST_CONFIG=true` |
| `VERIRELAY_ALLOW_TEST_CONFIG` | `false` | 允许本地运行配置 source |
| `VERIRELAY_FAKE_PROVISIONING_QUOTE` | `false` | 向 VeriProv 发送模拟证明报告，需要 `VERIRELAY_ALLOW_TEST_CONFIG=true`；真实 VeriProv 会拒绝 |

### 收据 store

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VERIRELAY_RECEIPT_STORE_TYPE` | `memory` | `memory`、`redis` 或 `valkey`（等同 `redis`） |
| `VERIRELAY_RECEIPT_STORE_TTL` | `1h` | 收据 TTL，正 duration |
| `VERIRELAY_RECEIPT_STORE_MAX_ENTRIES` | `10000` | `memory` store 最多保留的收据数，正整数 |
| `VERIRELAY_RECEIPT_STORE_ADDR` | 空 | Redis/Valkey 地址；`redis`/`valkey` 时必填 |
| `VERIRELAY_RECEIPT_STORE_TLS` | `false` | 到 Redis/Valkey 启用 TLS |
| `VERIRELAY_RECEIPT_STORE_USERNAME` | 空 | Redis ACL username |
| `VERIRELAY_RECEIPT_STORE_PASSWORD_FILE` | 空 | 从文件读取 Redis/Valkey 密码 |
| `VERIRELAY_RECEIPT_STORE_DB` | `0` | Redis DB，非负整数 |
| `VERIRELAY_RECEIPT_STORE_CONNECT_TIMEOUT` | `5s` | 正 duration |
| `VERIRELAY_RECEIPT_STORE_READ_TIMEOUT` | `2s` | 正 duration |
| `VERIRELAY_RECEIPT_STORE_WRITE_TIMEOUT` | `2s` | 正 duration |
| `VERIRELAY_RECEIPT_STORE_KEY_PREFIX` | `verirelay:` | 非空 key prefix |

### 原文留存存储

未设置 `VERIRELAY_RAW_STORE_TYPE` 时，如果收据 store 是 Redis，原文留存与其共用，否则使用 memory store。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VERIRELAY_RAW_STORE_TYPE` | 空 | `memory`、`redis`、`valkey`（等同 `redis`）或 `disabled` |
| `VERIRELAY_RAW_STORE_MAX_BYTES` | `268435456` | `memory` store 最多保留的字节数，正整数 |
| `VERIRELAY_RAW_STORE_MAX_TTL` | 空 | 正 duration，限制客户端可请求的最长保存时长；为空表示不限 |
| `VERIRELAY_RAW_STORE_ADDR` | 空 | Redis/Valkey 地址；`redis`/`valkey` 时必填 |
| `VERIRELAY_RAW_STORE_TLS` | `false` | 到 Redis/Valkey 启用 TLS |
| `VERIRELAY_RAW_STORE_USERNAME` | 空 | Redis ACL username |
| `VERIRELAY_RAW_STORE_PASSWORD_FILE` | 空 | 从文件读取 Redis/Valkey 密码 |
| `VERIRELAY_RAW_STORE_DB` | `0` | Redis DB，非负整数 |
| `VERIRELAY_RAW_STORE_CONNECT_TIMEOUT` | `5s` | 正 duration |
| `VERIRELAY_RAW_STORE_READ_TIMEOUT` | `2s` | 正 duration |
| `VERIRELAY_RAW_STORE_WRITE_TIMEOUT` | `2s` | 正 duration |
| `VERIRELAY_RAW_STORE_KEY_PREFIX` | `verirelay:` | 非空 key prefix |
