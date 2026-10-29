[English](development.md) | 简体中文

# 开发

## 环境要求

- Go `1.26.10`（`go.mod` 的 `go` 指令）。
- Linux。二进制只能为 Linux 构建。
- Redis 与 Valkey，用于 storage 集成测试。

## 构建

```bash
go build ./...
```

## 测试

```bash
go vet ./...
go test ./...
go test -race ./internal/storage ./internal/proxy
```

Redis/Valkey 集成测试使用 `integration` build tag：

```bash
VERIRELAY_TEST_REDIS_ADDR=127.0.0.1:6379 go test -tags=integration ./internal/storage
VERIRELAY_TEST_VALKEY_ADDR=127.0.0.1:6380 go test -tags=integration ./internal/storage
```

## 确定性 init 构建

`scripts/build-init.sh` 是 init 二进制唯一的构建入口，本地与 CI 一致：

```bash
./scripts/build-init.sh [输出路径]
```

默认输出：`bin/verirelay-linux-amd64`。

脚本固定了工具链、环境和构建参数，产物逐字节可复现。配方的任何改动都会改变产物，必须同步更新 [release](https://github.com/verirelay/release) 仓库 `versions.lock` 中的 `[expected]` 基线。

## CI

`.github/workflows/ci.yml` 在 push 到 `main` 和 pull request 时运行：

1. gofumpt 格式检查
2. `go vet ./...`
3. `golangci-lint`
4. `go test ./...`
5. `build-init.sh` 构建，并断言产物是静态链接的 x86-64 ELF
6. 对 workflow 文件运行 `actionlint`

`make check` 在本地使用 CI 锁定的版本运行格式、vet、测试、构建和 lint。

## 发布

release workflow（`release.yml`）在推送 `v*` tag 或对已有 tag 手动 dispatch 时运行：

1. 用 `build-init.sh` 重新构建 init 二进制
2. 写出 `SHA256SUMS` 和组件构建报告（`verirelay-component-build.json`、`verirelay-component-build/1`）
3. 用 cosign keyless 签名产物
4. 附加 GitHub 原生 provenance attestation
5. 发布 GitHub release
