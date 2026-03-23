# MCP Auth Proxy (Go)

基于 `docs/mcp-gateway-auth-design.md` 实现的 Go 版本地 `MCP Auth Proxy`，用于给第三方智能体提供一个带 OAuth2 PKCE 登录、Token 缓存/刷新、MCP 请求转发能力的本地代理。

## 已实现能力

- 暴露本地 `POST /mcp`
- 暴露 `GET /auth/callback`
- 暴露 `GET /auth/status`
- 暴露 `POST /auth/login`
- 暴露 `POST /auth/logout`
- 使用 `Authorization Code + PKCE`
- 自动拉起浏览器登录
- 本地保存 Token，并在 Windows 上用 DPAPI 加密落盘
- Access Token 过期前自动刷新
- 上游网关返回 `401 token_expired` 时自动刷新并重试一次
- 支持从 `.env` 配置文件读取代理配置
- 内置可独立运行的 mock 鉴权中心

## 环境变量

- `MCP_PROXY_CONFIG_FILE`：可选，读取 `.env` 配置文件
- `MCP_PROXY_LISTEN_ADDR`：默认 `127.0.0.1:8765`
- `MCP_PROXY_UPSTREAM_MCP_URL`：必填，远端网关 `POST /mcp` 地址
- `MCP_PROXY_AUTHORIZE_URL`：必填，鉴权中心授权地址
- `MCP_PROXY_TOKEN_URL`：必填，鉴权中心 Token 地址
- `MCP_PROXY_CLIENT_ID`：必填，本地代理注册的 `client_id`
- `MCP_PROXY_SCOPE`：默认 `mcp.invoke mcp.read`
- `MCP_PROXY_REDIRECT_URL`：默认按监听地址生成，例如 `http://127.0.0.1:8765/auth/callback`
- `MCP_PROXY_CALLBACK_PATH`：默认 `/auth/callback`
- `MCP_PROXY_TOKEN_FILE`：Token 本地存储文件路径
- `MCP_PROXY_AUTO_OPEN_BROWSER`：默认 `true`
- `MCP_PROXY_REFRESH_SKEW`：默认 `60s`
- `MCP_PROXY_LOGIN_STATE_TTL`：默认 `10m`
- `MCP_PROXY_TOKEN_TIMEOUT`：默认 `15s`

## 运行方式

```powershell
$env:MCP_PROXY_UPSTREAM_MCP_URL="http://gateway.example.com/mcp"
$env:MCP_PROXY_AUTHORIZE_URL="http://auth.example.com/oauth2/authorize"
$env:MCP_PROXY_TOKEN_URL="http://auth.example.com/oauth2/token"
$env:MCP_PROXY_CLIENT_ID="local-mcp-proxy"
go run ./cmd/mcp-proxy
```

或使用配置文件：

```powershell
$env:MCP_PROXY_CONFIG_FILE="configs/proxy.env.example"
go run ./cmd/mcp-proxy
```

## Mock 鉴权中心

仓库内新增了一个可独立运行的 mock OAuth2 鉴权中心：

- 入口：`cmd/mock-auth-center`
- 授权端点：`GET /oauth2/authorize`
- Token 端点：`POST /oauth2/token`
- Introspect 端点：`POST /oauth2/introspect`

Mock 鉴权中心环境变量：

- `MOCK_AUTH_CONFIG_FILE`：可选，读取 `.env` 配置文件
- `MOCK_AUTH_LISTEN_ADDR`：默认 `127.0.0.1:18080`
- `MOCK_AUTH_ISSUER`：默认 `http://127.0.0.1:18080`
- `MOCK_AUTH_CLIENT_ID`：默认 `local-mcp-proxy`
- `MOCK_AUTH_DEFAULT_USER_ID`：默认 `u12345`
- `MOCK_AUTH_DEFAULT_USER_NAME`：默认 `zhangsan`
- `MOCK_AUTH_DEFAULT_SCOPE`：默认 `mcp.invoke mcp.read`
- `MOCK_AUTH_ACCESS_TTL`：默认 `5m`
- `MOCK_AUTH_REFRESH_TTL`：默认 `12h`
- `MOCK_AUTH_CODE_TTL`：默认 `2m`
- `MOCK_AUTH_INTERACTIVE`：默认 `false`
- `MOCK_AUTH_AUTO_APPROVE`：默认 `true`

运行示例：

```powershell
$env:MOCK_AUTH_CONFIG_FILE="configs/mock-auth.env.example"
go run ./cmd/mock-auth-center
```

## 本地联调

1. 启动 mock 鉴权中心：`go run ./cmd/mock-auth-center`
2. 启动代理：`go run ./cmd/mcp-proxy`
3. 访问 `http://127.0.0.1:8765/auth/status` 检查登录状态
4. 向 `POST http://127.0.0.1:8765/auth/login` 发请求，或直接让智能体请求 `/mcp`
5. 浏览器完成回调后，再次发起 MCP 请求

说明：`configs/proxy.env.example` 里的 `MCP_PROXY_UPSTREAM_MCP_URL` 仍需指向你的真实网关或本地 mock 网关。

## 第三方智能体接入

把 MCP 地址配置为：

```text
http://127.0.0.1:8765/mcp
```

首次访问若未登录，代理会返回结构化错误并自动拉起浏览器；完成登录后重试同一个 MCP 请求即可。
