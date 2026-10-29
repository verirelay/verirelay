[English](glossary.md) | 简体中文

# 术语表

## AVR

| 术语 | 定义 |
| --- | --- |
| AVR (Attested Verifiable Relay) | VeriRelay 的可验证转发机制：每次转发都由绑定到证明报告的密钥签发收据 |

## 组件

| 术语 | 定义 |
| --- | --- |
| VeriRelay | 运行在 CVM 中的透明代理，为每次转发签发收据，也是整个项目的名字 |
| VeriForge | 可复现地构建启动产物集的工具 |
| VeriProv | 配置下发服务：CVM 通过远程证明后，向它下发运行配置 |
| Registry | 可选服务，根据启动度量查询对应的构建身份（仓库、commit、版本） |
| Verifier | VeriRelay 的参考验证工具，任何人都能按 Protocol 自行实现验证 |
| Protocol | 公开的线格式定义：`avr/1`、`veriprov/1` 和 guest 文件 schema |
| Release | 发布流水线，产出启动产物集和发布证据链 |

## 角色

| 术语 | 定义 |
| --- | --- |
| 运营者（Operator） | 部署和运行 VeriRelay 的一方，包括持有 `admin_token` 的人 |
| 云厂商（Cloud Provider） | 提供带机密计算能力 CPU 的一方 |
| 上游（Upstream） | VeriRelay 转发请求的目标服务 |
| AI 中转站（AI Gateway） | 典型接入场景之一：替用户调用大模型 API 的第三方服务，VeriRelay 部署在它和上游之间 |

## 运行环境

| 术语 | 定义 |
| --- | --- |
| 机密虚拟机（CVM） | 运行 VeriRelay 的 SEV-SNP 机密虚拟机 |
| 可信执行环境（TEE） | 提供隔离和远程证明能力的可信执行环境 |
| Workload | 在受保护环境中运行的软件，以及它固定的启动内容 |
| VM | 泛指虚拟机 |
| Guest | 从虚拟化角度看，虚拟机内部的软件环境 |
| 实例（Instance） | 部署系统管理的一次具体 CVM 运行 |

## 启动与远程证明

| 术语 | 定义 |
| --- | --- |
| 启动产物（Boot Artifacts） | 启动 CVM 用到的文件：OVMF 固件、内核镜像（vmlinuz）、initrd；强调三者是同一版本配套发布时，叫启动产物集（Boot Artifact Set） |
| 来宾启动声明（Guest Manifest） | initrd 中的 `/etc/verirelay/manifest.json`，记录构建来源和 vCPU 数以外的启动参数 |
| 启动度量（Launch Measurement） | AMD SEV-SNP 在 CVM 启动时对启动产物集和启动参数计算出的哈希，写在证明报告里；它随 vCPU 数变化，所以同一个启动产物集对每种 vCPU 数各有一个启动度量 |
| 远程证明（Attestation） | 由 TEE 证明正在运行的 Workload 的身份和状态的过程 |
| 证明报告（Attestation Report） | 由 AMD 安全处理器签名的 SEV-SNP 报告，里面包含启动度量、`REPORT_DATA` 和 `HOST_DATA` |
| HOST_DATA | 证明报告中由宿主机在启动 CVM 时设置的 32 字节字段；对 VeriRelay 来说它是引导文件的 SHA-256，VeriProv 据此为 CVM 选择运行配置 |
| 证明报告原文（Attestation Evidence） | 证明接口响应中的 `evidence` 字段，即证明报告的原始内容 |
| Attestation Nonce | 验证者提供的 32 字节随机数，写进 `REPORT_DATA`，用来证明证明报告是新鲜生成的 |

## 收据

| 术语 | 定义 |
| --- | --- |
| 收据 | VeriRelay 每次转发后签发的 `avr/1` 记录，内容包括请求和响应的哈希、上游 TLS 身份；计量单位是「每次转发」 |
| 收据签名密钥 | 在 CVM 内生成、用来签发收据的 Ed25519 密钥，通过 `REPORT_DATA` 绑定到证明报告 |
| 收据证据包 | `/v1/avr/evidence` 的 `avr/1` 返回内容：收据，加上为其收据签名密钥归档的证明响应，没有归档时为 `null` |
| 原文留存（Raw Capture） | 可选功能：把请求和响应原文加密保存，之后凭密钥取回，再和收据核对 |

## 配置下发

| 术语 | 定义 |
| --- | --- |
| 配置下发（Provisioning） | CVM 通过远程证明后，VeriProv 用 `veriprov/1` 把运行配置加密下发给它的过程 |
| 运行配置（Runtime Config） | 下发给 VeriRelay 的敏感配置：`admin_token` 和 store |
| 引导文件（Bootstrap） | 配置盘上的文件，写明 VeriProv 地址和 VeriProv 身份密钥；它的 SHA-256 就是 CVM 的 `HOST_DATA` |
| 配置盘（Config Disk） | 挂载到 CVM 的只读 virtio 磁盘，存放引导文件 |
| VeriProv 身份密钥（VeriProv Identity Key） | VeriProv 长期使用的 Ed25519 密钥，用来给它下发的运行配置签名；引导文件写明它的公钥 |

## 发布与溯源

| 术语 | 定义 |
| --- | --- |
| 发布证据链（Release Evidence Chain） | 每个发布附带的 `chain.json`，把启动度量连接到各组件的源码 commit |
| 构建报告（Build Report） | VeriForge 每次构建写出的 `build-report.json`：来宾启动声明、固定输入和产物 digest；与构建记录不是一回事 |
| Release Key | Release 用来给签名构建记录及其撤销声明签名的 Ed25519 密钥 |
| 签名构建记录（Signed Build Record） | Registry 中用 Release Key 签名的记录，把启动度量映射到构建身份，简称构建记录（Build Record）；每个发布还把它的构建记录作为 release 资产 `build-records.json` 发布 |
