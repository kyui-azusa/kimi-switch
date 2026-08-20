# KimiSwitch 文档

这里记录 KimiSwitch 已确定的运行约定和仍在推进的设计，避免实现、界面和安装包之间出现不同理解。

## 文档索引

- [本地代理与端口约定](networking.md)：监听地址、CC Switch 端口避让和旧配置迁移。
- [账号健康、轮换与额度刷新](account-health.md)：失败隔离、恢复探测和刷新周期推算。
- [应用内更新与签名发布](updates.md)：更新检查、签名密钥、发布服务器和回滚。
- [接管配置覆盖与自动恢复](takeover-recovery.md)：外部配置写回导致接管关闭的根因、证据和恢复机制。

## 当前状态

| 项目 | 状态 | 跟踪位置 |
| --- | --- | --- |
| 默认代理端口改为 `127.0.0.1:15722` | 已在 0.1.2 分支实现 | [PR #3](https://github.com/kyui-azusa/kimi-switch/pull/3) |
| 冷却到期后先探测再恢复 | 待实现 | [Issue #1](https://github.com/kyui-azusa/kimi-switch/issues/1) |
| 记录并推算额度刷新时间 | 待实现 | [Issue #2](https://github.com/kyui-azusa/kimi-switch/issues/2) |
| Tauri 应用内更新与签名发布 | 已在 0.2.0 分支实现 | [Issue #4](https://github.com/kyui-azusa/kimi-switch/issues/4) |
| 外部配置写回后保持用户接管意图 | 已在 0.2.4 分支实现 | [Issue #10](https://github.com/kyui-azusa/kimi-switch/issues/10) |

## 边界

KimiSwitch 只管理用户有权使用的 Kimi Code API Key，提供本地切换、轮换、故障转移和可观测性。它不用于绕过额度、并发限制、计费规则或服务方的反滥用机制。
