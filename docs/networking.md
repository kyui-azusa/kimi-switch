# 本地代理与端口约定

## 默认地址

KimiSwitch 的默认监听地址为：

```text
127.0.0.1:15722
```

客户端使用的 OpenAI 兼容地址为：

```text
http://127.0.0.1:15722/v1
```

代理只监听回环地址，不应默认暴露到局域网或公网。

## 为什么不用 15721

CC Switch 默认使用 `127.0.0.1:15721`。KimiSwitch 如果复用该端口，两款应用同时启动时会出现端口占用，后启动的一方无法启动本地代理。

因此从 KimiSwitch 0.1.2 起：

- CC Switch 保持使用默认端口 `15721`。
- KimiSwitch 默认使用独立端口 `15722`。
- 两款应用可以在同一台机器上同时运行。

## 旧配置迁移

桌面端启动时，如果发现 KimiSwitch 配置中的监听地址仍是旧默认值 `127.0.0.1:15721`，会将它改写为 `127.0.0.1:15722`。

迁移只改变监听地址，不删除或重建以下数据：

- 已导入账号；
- 系统凭据库中的 API Key；
- 当前账号和轮换设置；
- 请求、Token 和健康检查统计。

## 修改端口时需要同步的位置

默认端口同时参与后端监听、桌面端启动探测、前端 API 请求和 Tauri CSP。以后再次调整时必须同步检查：

- `internal/config/config.go`
- `src-tauri/src/lib.rs`
- `src-tauri/tauri.conf.json`
- `src/lib/api.ts`
- `src/App.tsx`
- `config.example.json`
- `README.md`

至少运行以下检查：

```sh
go test -race ./...
go vet ./...
pnpm run build
cargo check --manifest-path src-tauri/Cargo.toml
```
