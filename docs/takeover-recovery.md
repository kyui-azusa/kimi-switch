# 接管配置覆盖与自动恢复

## 用户遇到的问题

用户在 KimiSwitch 中明确开启接管后，顶栏开关会在新开 Kimi Code 对话或配置刷新后突然变为“Kimi Code 直连”。新会话或执行 `/reload` 后可能绕过本地代理，也可能出现：

```text
Error: [config.invalid] Model "kimi-code/k3-256k" is not configured in config.toml.
```

这不是单纯的开关显示问题。已经运行的 Kimi Code 会话可能继续使用内存中的旧配置，但新会话会读取磁盘上的直连配置，接管实际已经丢失。

## 定位证据

- `~/.kimi-code/config.toml` 曾被原子替换，创建时间和修改时间完全相同。
- 用户重新开启接管后，文件内容与 KimiSwitch 之前生成的托管快照逐字节相同。
- KimiSwitch UI 每 5 秒读取一次 `/api/status`，没有后台定时调用关闭接口。
- 代码中只有 `PUT /api/kimi/takeover {"enabled":false}` 会主动关闭接管。
- 旧实现没有保存用户期望的接管状态，只通过 `config.toml` 中的托管标记推断开关。

“信任新文件夹”本身主要修改 `~/.kimi-code/workspaces.json`，并不必然触发问题。风险来自同一时间发生的配置迁移、模型设置保存或其他整文件写回：如果写回基于旧快照，KimiSwitch 的注释标记和本地 provider 会一起消失。

## 根因

旧状态模型把外部文件同时当成配置载体和唯一事实源：

```text
用户开启接管
    -> KimiSwitch 改写 config.toml
    -> 外部程序整文件写回直连配置
    -> 托管标记消失
    -> Inspect() 返回 enabled=false
    -> UI 下一次轮询显示关闭
    -> 自动修复因为 enabled=false 而直接退出
```

0.2.3 的自愈仅处理“托管标记仍存在，但 TOML 无效”的情况，无法区分“用户明确关闭”和“外部程序覆盖”。

## 0.2.4 解决方案

KimiSwitch 在自己的 `config.json` 中持久化 `takeover_enabled`，将用户意图与外部文件状态分离：

- 旧版配置没有该字段时，首次状态检查从当前有效配置迁移，避免升级后误关接管。
- 用户通过开关明确启用或关闭后，只有 Kimi 配置切换成功才保存新意图。
- 期望开启但托管块消失、模型恢复直连或配置损坏时，状态同步自动重建完整托管配置。
- 期望关闭但文件仍引用 `kimi-switch-local` 时，状态同步恢复原 provider。
- 继续保留首次接管备份、重复 provider 清理、原子写入和写入前 TOML 校验。

## 回归验证

- 用合法直连配置覆盖已接管的 `config.toml`，下一次状态刷新恢复接管。
- 从 0.2.3 的无字段配置升级，当前接管状态被迁移并写入 KimiSwitch 配置。
- 用户明确关闭后，即使托管配置再次出现并重启 Manager，也会恢复直连。
- UI 使用的 `PUT /api/kimi/takeover` 会同时持久化开启和关闭选择。

跟踪工单：[Issue #10](https://github.com/kyui-azusa/kimi-switch/issues/10)。
