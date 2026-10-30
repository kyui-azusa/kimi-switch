<div align="center">

<img src="assets/branding/kimi-switch-icon-master.png" width="96" alt="KimiSwitch 图标">

# KimiSwitch

**Kimi Code 额度用完了？不用停 —— 一键切换到另一个账号继续干活。**

KimiSwitch 是一个桌面应用：把你名下的多个 Kimi Code API Key 汇进一个本地代理，
当前账号额度耗尽、触发限流或出现故障时自动换号，请求原地重发，会话不中断。

**[下载 macOS / Windows 安装包](https://github.com/kyui-azusa/kimi-switch/releases/latest)** · [English](README.md)

[![GitHub Release](https://img.shields.io/github/v/release/kyui-azusa/kimi-switch)](https://github.com/kyui-azusa/kimi-switch/releases/latest)
[![CI](https://github.com/kyui-azusa/kimi-switch/actions/workflows/ci.yml/badge.svg)](https://github.com/kyui-azusa/kimi-switch/actions/workflows/ci.yml)
[![下载量](https://img.shields.io/github/downloads/kyui-azusa/kimi-switch/total)](https://github.com/kyui-azusa/kimi-switch/releases)
[![License: MIT](https://img.shields.io/github/license/kyui-azusa/kimi-switch)](LICENSE)
[![平台](https://img.shields.io/badge/platform-macOS%20%7C%20Windows-lightgrey)](https://github.com/kyui-azusa/kimi-switch/releases/latest)

<!-- TODO: 补充 docs/screenshot.png —— 账号列表与用量页截图 -->

</div>

## 为什么需要 KimiSwitch

Kimi Code 对每个账号都有 5 小时窗口和周窗口的额度限制。当前账号额度用完时，常见的做法是
手动编辑 `~/.kimi-code/config.toml`、换 Key、重启会话。KimiSwitch 把这套流程自动化：

- **Kimi 多账号一键切换** —— 导入你现有的 Kimi 配置，在桌面界面里点一下即切换当前账号。
- **额度耗尽自动换号** —— 开启自动轮换后，账号额度耗尽、被限流或失效时自动切到健康账号，并在原地重发请求，正在进行的 Kimi Code 会话尽量不被打断。
- **诚实的额度观** —— 不抓取未公开的配额页面，不伪造客户端身份，不声称本地观测用量等于账号剩余配额。

## 功能

- 本地回环代理运行在 `127.0.0.1:15722`（避开 CC Switch 默认的 `15721` 端口）。
- API Key 存放在环境变量或系统凭据库（macOS 钥匙串 / Windows 凭据管理器），不出现在命令行参数里。
- 手动切换当前账号；可选的自动轮换模式：
  - `sticky`：持续使用当前 Key，仅在额度耗尽、鉴权失败、触发限流或上游不可用时才换号。
- 仅在轮换开启且响应流尚未成功开始时，才对上游 401 / 403 / 429 / 5xx 做重试。上游短时 429 会在同一 HTTP 请求内等待后重发，尽量保持 Kimi Code 原会话继续执行。
- 故障分类：额度耗尽、临时限流、Key 无效、账号停用、网络故障、上游故障。网络故障不会污染账号健康状态。
- 失败账号至少冷却 10 分钟；健康账号永远优先。
- 账号可用性检查使用一个 output token 的真实补全请求，"仅鉴权成功"无法掩盖额度已耗尽的账号。
- 按 Key、按天记录请求数、成功、失败、输入、输出、缓存读取、推理 token 等统计，提供 7/30 天趋势。
- 独立的 Kimi Code 直连 / KimiSwitch 接管开关：接管只修改 `~/.kimi-code/config.toml` 中的模型路由，保留原 provider，写入 `config.toml.kimiswitch.bak` 一次性备份，可原子恢复直连。
- 关闭窗口后在系统托盘继续运行；应用内签名校验自动更新。

## 下载

从 [最新 Release](https://github.com/kyui-azusa/kimi-switch/releases/latest) 下载安装包：

| 平台 | 安装包 |
| --- | --- |
| macOS Apple Silicon | `KimiSwitch_<版本号>_darwin-aarch64.dmg` |
| macOS Intel | `KimiSwitch_<版本号>_darwin-x86_64.dmg` |
| Windows x64 | `KimiSwitch_<版本号>_windows-x86_64-setup.exe` |

首次手动安装后，后续版本可在设置页内直接更新。完整发布历史见 [CHANGELOG.md](CHANGELOG.md)。

## 上手四步

1. 启动 KimiSwitch，本地代理自动运行在 `127.0.0.1:15722`。
2. 点击**导入**，从现有 `~/.kimi-code/config.toml` 读取 `api_key` 存入系统凭据库。导入是只读的；纯 OAuth 的 provider 会被跳过。
3. 打开 **KimiSwitch 接管**。模型请求改走本地代理；原 provider 完整保留，并写入一次性备份。已在运行的 Kimi Code 会话里执行 `/reload` 即可生效。
4. 可选：开启**自动轮换**（`sticky` 模式）。当前账号失效时自动换到下一个健康账号并重发请求。

想退回直连，关掉接管开关即可，原 provider 引用会被恢复。

## 和替代方案对比

| | 手动改 `config.toml` | [CC Switch](https://github.com/farion1231/cc-switch) | KimiSwitch |
| --- | --- | --- | --- |
| 切换当前 Kimi 账号 | 改文件 + 重启会话 | 一键 | 一键 |
| 额度耗尽自动换号 | 无 | 无 | 有，且原地重试请求 |
| 区分额度耗尽 / 临时限流 | 无 | 无 | 有 |
| Kimi 按账号 token 用量统计 | 无 | 无 | 有（输入 / 输出 / 缓存读取 / 推理） |
| Key 存储 | 配置文件明文 | 配置文件明文 | 系统凭据库或环境变量 |
| 应用内签名自动更新 | — | 有 | 有，带备用更新源 |

KimiSwitch 的桌面信息架构、视觉令牌和部分 UI 封装改编自 MIT 许可的 CC Switch ——
见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。多账号池化、故障转移代理和 Kimi 专属的用量记账为原创实现。

## KimiSwitch 不会做的事

- 不会创造额度。它只在你**自己名下已授权的账号**之间轮换，每个账号的套餐限制不变。
- 不抓取未公开的配额页面或非官方接口。
- 不伪造客户端身份，除替换 API Key 外不修改请求内容。
- 不把本地观测用量当作账号剩余配额。手工记录的刷新时间一律标注为 `手动参考 · 非官方`，绝不参与轮换、健康度或额度判断。

KimiSwitch 仅供个人或已授权的内部使用，与 Moonshot AI、Kimi 及 CC Switch 项目均无隶属关系。

## 常见问题

**切换账号会打断正在运行的 Kimi Code 会话吗？**
不会。接管只修改 `config.toml` 的模型路由，会话内执行 `/reload` 即可（新会话自动生效）。轮换换号时代理会尽量在同一 HTTP 连接内用新账号重发请求。

**我的 API Key 存在哪里？**
环境变量或系统凭据库（macOS 钥匙串 / Windows 凭据管理器），从不出现在命令行参数中。代理转发时请求体只存在于内存；持久化状态只包含元数据和用量计数，不保存任何提示词或响应内容。

**支持 Windows 吗？**
支持。每次发布都提供 macOS（Apple Silicon / Intel）和 Windows x64 安装包。

**所有账号都被限流了怎么办？**
代理会直接返回上游原始错误，而不是无限重试。冷却中的账号只有在没有任何健康账号可用时才会被重试。

**如何更新？**
设置页内一键检查更新：应用拉取签名 manifest、校验 minisign 签名、下载安装并重启。主更新源是自建静态服务器，GitHub Release 作为自动回退备用源。

## 开发

需要 Node 22、pnpm、Go 1.24 和 Rust 工具链。

```sh
cd kimi-switch
pnpm install
pnpm tauri:dev
```

Tauri 应用会把 Go 代理作为 sidecar 一起构建启动。CLI 仍可用于脚本化操作：

```sh
go run ./cmd/kimi-switch init
go run ./cmd/kimi-switch add work --env KIMI_WORK_KEY
export KIMI_WORK_KEY='your-work-key'
go run ./cmd/kimi-switch rotation on sticky
go run ./cmd/kimi-switch serve
```

管理端点（统一使用本地代理 bearer key，默认 `local-kimi-switch`）：

```text
GET  /healthz
GET  /api/status
GET  /api/usage
PUT  /api/kimi/takeover          {"enabled":true}
POST /api/rotation              {"enabled":true,"mode":"sticky"}
POST /api/keys/<name>/use
POST /api/keys/<name>/test
```

验证：

```sh
go test ./...
go vet ./...
pnpm test:update-manifest
```

## 文档

- [docs/README.md](docs/README.md) —— 行为与设计决策
- [docs/updates.md](docs/updates.md) —— 更新签名、双更新源、发布服务器部署与回滚
- [docs/account-health.md](docs/account-health.md) —— 故障分类与冷却规则
- [docs/takeover-recovery.md](docs/takeover-recovery.md) —— 接管状态恢复
- [CHANGELOG.md](CHANGELOG.md) —— 发布历史
- [CONTRIBUTING.md](CONTRIBUTING.md) —— 开发环境与提交前检查
- [SECURITY.md](SECURITY.md) —— 安全模型与漏洞报告

## 参与贡献

欢迎提 issue 和 PR，中英文皆可。[CONTRIBUTING.md](CONTRIBUTING.md) 说明了开发环境、目录结构，
以及每个 PR 在 CI 上会跑的检查。

发现安全问题请不要公开提 issue，按 [SECURITY.md](SECURITY.md) 的方式私下反馈。

## 许可证

KimiSwitch 以 [MIT 许可证](LICENSE)发布。CC Switch 是独立项目，有其自身的许可证与署名要求，
见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
