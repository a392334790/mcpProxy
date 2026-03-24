# MCP Auth Proxy 接口设计稿

## 1. 设计目标

- 统一 `POST /mcp`、`POST /auth/login`、`GET /auth/status`、`POST /auth/logout` 的错误语义
- 保持 `POST /mcp` 成功响应对上游 MCP 的透明透传
- 给智能体和用户同时提供可读、可执行的登录指引
- 为后续 `trace_id`、按钮化引导、状态轮询留出扩展点

## 2. 统一响应信封

管理接口以及 `POST /mcp` 的代理侧失败统一返回以下结构：

```json
{
  "success": true,
  "code": "ok",
  "message": "Operation succeeded",
  "display_message": "操作成功",
  "trace_id": "b7b6d4b8d4e54b5d",
  "timestamp": "2026-03-24T10:20:30+08:00",
  "data": {}
}
```

失败时：

```json
{
  "success": false,
  "code": "auth_required",
  "message": "Authentication required",
  "display_message": "访问企业 MCP 需要先登录，请完成浏览器登录后重试。",
  "trace_id": "b7b6d4b8d4e54b5d",
  "timestamp": "2026-03-24T10:20:30+08:00",
  "data": {}
}
```

## 3. 字段说明

- `success`：操作是否成功
- `code`：稳定错误码或业务码，供智能体识别逻辑
- `message`：技术描述，适合日志和排障
- `display_message`：建议直接展示给用户的中文提示
- `trace_id`：请求追踪 ID，同时通过 `X-Trace-Id` 响应头返回
- `timestamp`：响应生成时间
- `data`：业务数据主体

## 4. 错误码

- `ok`
- `already_logged_in`
- `login_started`
- `login_in_progress`
- `logout_success`
- `auth_required`
- `token_expired`
- `invalid_token`
- `auth_service_unavailable`
- `upstream_unavailable`
- `login_start_failed`
- `logout_failed`
- `invalid_request`
- `method_not_allowed`
- `proxy_failed`

## 5. 接口定义

### 5.1 `GET /auth/status`

用途：查询本地代理当前登录状态。

已登录示例：

```json
{
  "success": true,
  "code": "ok",
  "message": "Login status loaded",
  "display_message": "已登录",
  "trace_id": "7e21d3d7b9c44c12",
  "timestamp": "2026-03-24T10:30:00+08:00",
  "data": {
    "logged_in": true,
    "pending_login": false,
    "status_url": "http://127.0.0.1:8765/auth/status",
    "user_id": "u12345",
    "user_name": "zhangsan",
    "scope": "mcp.invoke mcp.read",
    "expires_at": "2026-03-24T11:00:00+08:00"
  }
}
```

登录中示例：

```json
{
  "success": true,
  "code": "login_in_progress",
  "message": "Login is in progress",
  "display_message": "检测到登录流程正在进行，请在浏览器完成登录后重试。",
  "trace_id": "7e21d3d7b9c44c12",
  "timestamp": "2026-03-24T10:30:00+08:00",
  "data": {
    "logged_in": false,
    "pending_login": true,
    "status_url": "http://127.0.0.1:8765/auth/status",
    "login_url": "http://127.0.0.1:8765/auth/login",
    "auth_url": "http://127.0.0.1:18080/oauth2/authorize?..."
  }
}
```

### 5.2 `POST /auth/login`

用途：发起或复用一次浏览器登录流程。

- 已登录：返回 `already_logged_in`
- 已存在登录流程：返回 `login_in_progress`
- 新发起登录：返回 `login_started`

新发起登录示例：

```json
{
  "success": true,
  "code": "login_started",
  "message": "Login flow started",
  "display_message": "请在浏览器完成登录，然后回到智能体重试。",
  "trace_id": "42d2d52a5f734e6b",
  "timestamp": "2026-03-24T10:31:00+08:00",
  "data": {
    "opened": true,
    "auth_url": "http://127.0.0.1:18080/oauth2/authorize?...",
    "status_url": "http://127.0.0.1:8765/auth/status",
    "callback_path": "/auth/callback",
    "expires_at": "2026-03-24T10:41:00+08:00",
    "next_step": "完成登录后重新发起 MCP 请求"
  }
}
```

### 5.3 `POST /auth/logout`

用途：清理本地 Token 和待处理登录状态。

成功示例：

```json
{
  "success": true,
  "code": "logout_success",
  "message": "Logged out",
  "display_message": "已退出登录。",
  "trace_id": "30ab2f5b6b764f44",
  "timestamp": "2026-03-24T10:35:00+08:00",
  "data": {
    "logged_in": false
  }
}
```

### 5.4 `GET /auth/callback`

用途：接收浏览器回调。

- 继续保持 HTML 页面返回，不包 JSON 信封
- 成功页：`登录成功，请回到智能体重新发起请求。`
- 失败页：根据原因分别提示登录过期、状态校验失败、Token 保存失败等

### 5.5 `POST /mcp`

用途：第三方智能体唯一访问的 MCP 入口。

#### 成功

- 原样透传上游 MCP 响应
- 不额外包一层 `success/code/data`
- 保持对 MCP Streamable HTTP / JSON-RPC 的兼容

#### 失败

代理侧认证失败时返回统一错误信封。

未登录示例：

```json
{
  "success": false,
  "code": "auth_required",
  "message": "Authentication required",
  "display_message": "访问企业 MCP 需要先登录，请完成浏览器登录后重试。",
  "trace_id": "c5dca1dd6c2b4f1a",
  "timestamp": "2026-03-24T10:40:00+08:00",
  "data": {
    "login_url": "http://127.0.0.1:8765/auth/login",
    "status_url": "http://127.0.0.1:8765/auth/status",
    "auth_url": "http://127.0.0.1:18080/oauth2/authorize?...",
    "opened": true,
    "retryable": true,
    "next_step": "完成登录后重新发起当前请求",
    "callback_path": "/auth/callback"
  }
}
```

Token 过期示例：

```json
{
  "success": false,
  "code": "token_expired",
  "message": "Access token expired",
  "display_message": "登录状态已过期，请重新登录后重试。",
  "trace_id": "c5dca1dd6c2b4f1a",
  "timestamp": "2026-03-24T10:40:00+08:00",
  "data": {
    "login_url": "http://127.0.0.1:8765/auth/login",
    "status_url": "http://127.0.0.1:8765/auth/status",
    "auth_url": "http://127.0.0.1:18080/oauth2/authorize?...",
    "opened": true,
    "retryable": true,
    "next_step": "完成登录后重新发起当前请求"
  }
}
```

## 6. 状态码建议

- `GET /auth/status`：固定 `200`
- `POST /auth/login`
  - `200`：已登录
  - `202`：新发起登录 / 已有登录流程
  - `500`：本地创建登录流程失败
- `POST /auth/logout`
  - `200`：退出成功
  - `500`：退出失败
- `POST /mcp`
  - `200`：上游 MCP 成功透传
  - `401`：`auth_required`、`token_expired`、`invalid_token`
  - `502`：`proxy_failed`
  - `503`：`auth_service_unavailable`、`upstream_unavailable`

## 7. 落地原则

- 管理接口 `/auth/*` 使用统一 JSON 信封
- 代理接口 `/mcp` 成功保持透传，失败统一输出结构化错误
- 所有失败响应都要同时提供：错误码、展示文案、下一步动作、状态查询入口
- 通过 `X-Trace-Id` 和响应体 `trace_id` 支持排障
