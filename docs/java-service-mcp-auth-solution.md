# Java存量服务转化为MCP服务并接入公司权限控制技术方案

## 文档信息

| 项目 | 内容 |
|------|------|
| 项目名称 | MCP Gateway 权限集成与Java服务MCP化改造 |
| 版本 | v1.0 |
| 日期 | 2026-03-23 |
| 文档类型 | 技术方案 |

---

## 1. 背景与目标

### 1.1 背景

随着AI智能体技术的快速发展，Model Context Protocol (MCP) 作为连接AI模型与外部工具/数据源的标准协议，正在成为企业AI应用集成的重要基础设施。公司内部存在大量成熟的Java存量服务，这些服务承载着核心业务逻辑和数据，需要将其能力开放给第三方智能体使用。

同时，企业级应用必须具备完善的权限控制体系，确保：
- 只有授权用户才能访问服务
- 敏感数据和操作受到保护
- 所有访问行为可审计

### 1.2 目标

1. **服务MCP化**：将现有Java存量服务转化为MCP服务，使其能够被AI智能体调用
2. **统一鉴权**：将公司内部鉴权体系接入MCP Gateway
3. **透明认证**：第三方智能体无需理解公司认证协议，通过本地代理完成认证
4. **身份透传**：网关校验Token后，将用户身份安全透传给下游服务
5. **可扩展性**：为后续tool级别授权、审计、限流留好扩展点

---

## 2. 现状分析

### 2.1 现有系统架构

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  第三方智能体    │────▶│  MCP Gateway    │────▶│  下游MCP服务     │
└─────────────────┘     └─────────────────┘     └─────────────────┘
                              │
                              ▼
                        ┌─────────────────┐
                        │  公司鉴权中心    │
                        └─────────────────┘
```

### 2.2 现有能力

| 组件 | 现有能力 |
|------|----------|
| MCP Gateway | 基于Spring Cloud Gateway + WebFlux，已具备基础AuthFilter |
| 鉴权中心 | 提供OAuth2授权、Token校验能力 |
| 下游服务 | 已注册到Nacos，可通过网关访问 |

### 2.3 存在问题

1. **Java服务未MCP化**：现有Java服务使用REST API，未适配MCP协议
2. **鉴权能力不足**：当前AuthFilter仅返回布尔值，无法透传用户身份
3. **错误处理简陋**：失败时只返回空401，无法指导上游做后续动作
4. **无本地代理**：第三方智能体无法自主完成OAuth回调和Token保存
5. **无Token缓存**：每个请求都打到鉴权中心，性能和可用性存在风险

---

## 3. 总体架构设计

### 3.1 架构角色

系统包含以下六个核心角色：

| 角色 | 职责 |
|------|------|
| 第三方智能体 | MCP Consumer，不直接参与OAuth登录流程 |
| 本地MCP Auth Proxy | 浏览器登录、Token存储、自动刷新、请求转发 |
| 系统浏览器 | 用户登录界面，完成OAuth授权 |
| 公司鉴权中心 | 提供授权、换Token、校验Token等OAuth2能力 |
| MCP Gateway | 校验Bearer Token，提取身份信息，注入身份头转发 |
| 下游MCP服务 | 接收网关转发请求，按用户身份做授权控制 |

### 3.2 架构图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              请求主链路 (流程1)                               │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐    ┌──────────┐  │
│  │ 🤖 第三方智能体 │───▶│ 🔐 本地Proxy  │───▶│ 🚪 Gateway   │───▶│ ⚙️ MCP服务│  │
│  │              │    │              │    │              │    │          │  │
│  │ MCP Consumer │    │ Token管理     │    │ Token校验    │    │ 业务处理  │  │
│  └──────────────┘    └──────────────┘    └──────────────┘    └──────────┘  │
│         │                   │                    │                          │
│         │                   │                    │                          │
└─────────┼───────────────────┼────────────────────┼──────────────────────────┘
          │                   │                    │
          │    ┌──────────────┴──────────────┐     │
          │    │     OAuth登录流程 (流程2)    │     │
          │    │                             │     │
          │    │  ┌──────────┐    ┌────────┐ │     │
          │    │  │ 🌐 浏览器 │◀──▶│ 🔑鉴权  │ │     │
          │    │  │          │    │  中心   │ │     │
          │    │  │ 用户登录  │    │OAuth2  │ │     │
          │    │  └──────────┘    └────────┘ │     │
          │    └─────────────────────────────┘     │
          │                                        │
          │         Token校验流程 (流程3)           │
          │    ┌─────────────────────────────┐     │
          │    │                             │     │
          └────│  Gateway ◀─────────▶ 鉴权中心 │─────┘
               │        introspect          │
               └─────────────────────────────┘
```

### 3.3 三大核心流程

#### 流程1：请求主链路
```
第三方智能体 → 本地Auth Proxy → MCP Gateway → 下游MCP服务
```

#### 流程2：OAuth登录流程（首次访问无Token时触发）
```
本地Auth Proxy ↔ 系统浏览器 ↔ 公司鉴权中心
```

#### 流程3：Token校验流程（每次请求时执行）
```
MCP Gateway → 公司鉴权中心 (introspect)
```

---

## 4. Java存量服务MCP化改造方案

### 4.1 MCP协议概述

MCP (Model Context Protocol) 是一种标准化的协议，用于AI模型与外部工具/数据源之间的通信。主要概念：

| 概念 | 说明 |
|------|------|
| Tool | 可被AI调用的工具/函数 |
| Resource | 可被AI访问的数据资源 |
| Prompt | 预定义的提示模板 |
| Server | 提供Tools/Resources/Prompts的服务端 |

### 4.2 Java服务MCP化改造策略

#### 4.2.1 改造方式选择

| 方式 | 优点 | 缺点 | 适用场景 |
|------|------|------|----------|
| **SDK封装** | 改动小，快速上线 | 需要引入新依赖 | 现有服务快速改造 |
| **独立MCP层** | 对原服务零侵入 | 架构复杂度高 | 核心服务、无法修改的服务 |
| **框架集成** | 开发体验好 | 需要框架支持 | 新服务开发 |

**推荐方案**：采用SDK封装方式，对现有Java服务进行最小化改造。

#### 4.2.2 SDK封装方案

```java
// 原有REST Controller
@RestController
@RequestMapping("/api/users")
public class UserController {
    
    @GetMapping("/{id}")
    public User getUser(@PathVariable String id) {
        return userService.findById(id);
    }
    
    @PostMapping
    public User createUser(@RequestBody UserCreateRequest request) {
        return userService.create(request);
    }
}
```

```java
// MCP Tool 封装
@MCPServer(name = "user-service", version = "1.0.0")
@RestController
public class UserMCPController {
    
    @MCPTool(
        name = "get_user",
        description = "根据用户ID获取用户信息"
    )
    @MCPParam(name = "id", description = "用户ID", required = true)
    public User getUser(@RequestParam String id) {
        return userService.findById(id);
    }
    
    @MCPTool(
        name = "create_user", 
        description = "创建新用户"
    )
    public User createUser(
        @MCPParam(name = "name", description = "用户名", required = true) String name,
        @MCPParam(name = "email", description = "邮箱", required = true) String email
    ) {
        UserCreateRequest request = new UserCreateRequest(name, email);
        return userService.create(request);
    }
}
```

#### 4.2.3 MCP SDK核心组件

```
┌─────────────────────────────────────────────────────────────┐
│                    MCP SDK for Java                         │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ @MCPServer  │  │  @MCPTool   │  │ @MCPResource        │  │
│  │             │  │             │  │                     │  │
│  │ 服务注册     │  │ 工具定义    │  │ 资源定义            │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ @MCPParam   │  │ MCPRegistry │  │ MCPTransport        │  │
│  │             │  │             │  │                     │  │
│  │ 参数定义     │  │ 工具注册表   │  │ 传输层(HTTP/SSE)    │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              MCPRequestHandler                       │   │
│  │                                                      │   │
│  │  • 解析MCP请求                                        │   │
│  │  • 参数校验与转换                                      │   │
│  │  • 调用业务方法                                        │   │
│  │  • 序列化响应                                          │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 4.3 改造步骤

#### 第一阶段：基础设施搭建

1. **开发MCP SDK**
   - 定义注解：`@MCPServer`, `@MCPTool`, `@MCPResource`, `@MCPParam`
   - 实现MCP协议解析器
   - 实现HTTP/SSE传输层
   - 实现与Spring的集成

2. **配置MCP端点**
   ```yaml
   mcp:
     server:
       name: ${spring.application.name}
       version: ${project.version}
       endpoint: /mcp
     transport:
       type: http
       sse-enabled: true
   ```

#### 第二阶段：服务改造

1. **识别可暴露能力**
   - 分析现有API接口
   - 筛选适合AI调用的能力
   - 定义Tool描述和参数

2. **添加MCP注解**
   - 在现有Controller上添加`@MCPServer`
   - 在需要暴露的方法上添加`@MCPTool`
   - 添加参数描述`@MCPParam`

3. **注册到Nacos**
   - 配置服务元数据标识MCP能力
   - 网关自动发现MCP服务

#### 第三阶段：权限集成

1. **读取身份头**
   ```java
   @Component
   public class AuthContextInterceptor implements HandlerInterceptor {
       
       @Override
       public boolean preHandle(HttpServletRequest request, 
                               HttpServletResponse response, 
                               Object handler) {
           String userId = request.getHeader("X-User-Id");
           String userName = request.getHeader("X-User-Name");
           String tenantId = request.getHeader("X-Tenant-Id");
           String roles = request.getHeader("X-User-Roles");
           
           AuthContext context = new AuthContext(userId, userName, tenantId, roles);
           AuthContextHolder.set(context);
           
           return true;
       }
   }
   ```

2. **Tool级别授权**
   ```java
   @MCPTool(
       name = "delete_user",
       description = "删除用户",
       requiredRoles = {"admin", "user_manager"}
   )
   public void deleteUser(@RequestParam String id) {
       // 业务逻辑
   }
   ```

### 4.4 改造示例

#### 用户服务改造示例

**改造前 (REST API)**:
```java
@RestController
@RequestMapping("/api/users")
public class UserController {
    
    @Autowired
    private UserService userService;
    
    @GetMapping("/{id}")
    public ResponseEntity<User> getUser(@PathVariable String id) {
        User user = userService.findById(id);
        return ResponseEntity.ok(user);
    }
    
    @GetMapping
    public ResponseEntity<Page<User>> listUsers(
            @RequestParam(defaultValue = "0") int page,
            @RequestParam(defaultValue = "10") int size) {
        Page<User> users = userService.findAll(page, size);
        return ResponseEntity.ok(users);
    }
    
    @PostMapping
    public ResponseEntity<User> createUser(@RequestBody UserCreateRequest request) {
        User user = userService.create(request);
        return ResponseEntity.status(HttpStatus.CREATED).body(user);
    }
    
    @PutMapping("/{id}")
    public ResponseEntity<User> updateUser(
            @PathVariable String id, 
            @RequestBody UserUpdateRequest request) {
        User user = userService.update(id, request);
        return ResponseEntity.ok(user);
    }
    
    @DeleteMapping("/{id}")
    public ResponseEntity<Void> deleteUser(@PathVariable String id) {
        userService.delete(id);
        return ResponseEntity.noContent().build();
    }
}
```

**改造后 (MCP + REST)**:
```java
@MCPServer(
    name = "user-service",
    version = "1.0.0",
    description = "用户管理服务"
)
@RestController
@RequestMapping("/api/users")
public class UserController {
    
    @Autowired
    private UserService userService;
    
    @MCPTool(
        name = "get_user",
        description = "根据用户ID获取用户详细信息，包括姓名、邮箱、部门等"
    )
    @GetMapping("/{id}")
    public ResponseEntity<User> getUser(
            @MCPParam(name = "id", description = "用户唯一标识ID", required = true)
            @PathVariable String id) {
        User user = userService.findById(id);
        return ResponseEntity.ok(user);
    }
    
    @MCPTool(
        name = "search_users",
        description = "搜索用户列表，支持按姓名、部门等条件筛选"
    )
    @GetMapping
    public ResponseEntity<Page<User>> listUsers(
            @MCPParam(name = "keyword", description = "搜索关键词，匹配姓名或邮箱")
            @RequestParam(required = false) String keyword,
            @MCPParam(name = "department", description = "部门ID")
            @RequestParam(required = false) String department,
            @MCPParam(name = "page", description = "页码，从0开始")
            @RequestParam(defaultValue = "0") int page,
            @MCPParam(name = "size", description = "每页数量")
            @RequestParam(defaultValue = "10") int size) {
        Page<User> users = userService.search(keyword, department, page, size);
        return ResponseEntity.ok(users);
    }
    
    @MCPTool(
        name = "create_user",
        description = "创建新用户账号"
    )
    @PostMapping
    public ResponseEntity<User> createUser(
            @RequestBody @Valid UserCreateRequest request) {
        User user = userService.create(request);
        return ResponseEntity.status(HttpStatus.CREATED).body(user);
    }
    
    @MCPTool(
        name = "update_user",
        description = "更新用户信息"
    )
    @PutMapping("/{id}")
    public ResponseEntity<User> updateUser(
            @MCPParam(name = "id", description = "用户ID", required = true)
            @PathVariable String id, 
            @RequestBody @Valid UserUpdateRequest request) {
        User user = userService.update(id, request);
        return ResponseEntity.ok(user);
    }
    
    @MCPTool(
        name = "delete_user",
        description = "删除用户账号（需要管理员权限）",
        requiredRoles = {"admin", "user_manager"}
    )
    @DeleteMapping("/{id}")
    public ResponseEntity<Void> deleteUser(
            @MCPParam(name = "id", description = "要删除的用户ID", required = true)
            @PathVariable String id) {
        userService.delete(id);
        return ResponseEntity.noContent().build();
    }
}
```

---

## 5. 权限控制接入方案

### 5.1 认证协议选择

**推荐协议**：OAuth2 Authorization Code + PKCE

**选择理由**：
1. 安全性高：授权码不直接暴露Token
2. 防止授权码拦截：PKCE机制防止code被窃取
3. 标准化：业界广泛采用，兼容性好
4. 支持刷新：Refresh Token支持长期会话

**不推荐方案**：
- 浏览器直接把access_token放到回调URL中
- 第三方智能体自己保存公司Token
- 仅依赖内网IP、Nacos注册信息作为身份认证依据

### 5.2 本地MCP Auth Proxy设计

#### 5.2.1 核心模块

```
┌─────────────────────────────────────────────────────────────┐
│                  本地 MCP Auth Proxy                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────┐  ┌─────────────────┐                  │
│  │ MCP转发模块      │  │ Token状态管理    │                  │
│  │                 │  │                 │                  │
│  │ • POST /mcp     │  │ • 检查Token存在  │                  │
│  │ • 请求透传      │  │ • 判断Token过期  │                  │
│  │ • 响应转发      │  │ • 判断需要刷新   │                  │
│  └─────────────────┘  └─────────────────┘                  │
│                                                             │
│  ┌─────────────────┐  ┌─────────────────┐                  │
│  │ OAuth2 PKCE模块  │  │ 回调接收模块     │                  │
│  │                 │  │                 │                  │
│  │ • 生成code_     │  │ • GET /auth/    │                  │
│  │   verifier      │  │   callback      │                  │
│  │ • 计算code_     │  │ • 校验state     │                  │
│  │   challenge     │  │ • 返回成功页面   │                  │
│  │ • 生成state     │  └─────────────────┘                  │
│  │ • 拉起浏览器    │                                        │
│  └─────────────────┘                                        │
│                                                             │
│  ┌─────────────────┐  ┌─────────────────┐                  │
│  │ Token交换刷新    │  │ 安全存储模块     │                  │
│  │                 │  │                 │                  │
│  │ • code换token   │  │ • Windows:      │                  │
│  │ • refresh_token │  │   Credential    │                  │
│  │   刷新          │  │   Manager       │                  │
│  │ • 更新缓存      │  │ • macOS: Keychain│                  │
│  └─────────────────┘  │ • Linux: Secret  │                  │
│                       │   Service        │                  │
│  ┌─────────────────┐  └─────────────────┘                  │
│  │ 自动重试模块     │                                        │
│  │                 │  ┌─────────────────┐                  │
│  │ • 401自动刷新   │  │ 运维接口模块     │                  │
│  │ • 刷新后重放    │  │                 │                  │
│  │ • 防止重复重放   │  │ • GET /auth/    │                  │
│  └─────────────────┘  │   status        │                  │
│                       │ • POST /auth/   │                  │
│                       │   login         │                  │
│                       │ • POST /auth/   │                  │
│                       │   logout        │                  │
│                       └─────────────────┘                  │
└─────────────────────────────────────────────────────────────┘
```

#### 5.2.2 Token数据结构

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "dGhpcyBpcyBhIHJlZnJlc2g...",
  "expires_at": 1777777777,
  "scope": "mcp.invoke mcp.read",
  "user_id": "u12345",
  "user_name": "zhangsan",
  "tenant_id": "t001"
}
```

#### 5.2.3 接口设计

| 接口 | 方法 | 说明 |
|------|------|------|
| `/mcp` | POST | MCP请求入口，第三方智能体唯一需要访问的地址 |
| `/auth/callback` | GET | 接收鉴权中心浏览器回调，参数：code, state |
| `/auth/status` | GET | 查询登录状态 |
| `/auth/login` | POST | 手动触发浏览器登录 |
| `/auth/logout` | POST | 删除本地Token，清空缓存 |

### 5.3 MCP Gateway改造方案

#### 5.3.1 改造架构

```
┌─────────────────────────────────────────────────────────────┐
│                    MCP Gateway 改造                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                    AuthFilter                        │   │
│  │                                                      │   │
│  │  1. 提取Bearer Token                                  │   │
│  │  2. 调用AuthCenterClient校验                          │   │
│  │  3. 成功：调用AuthHeaderEnricher注入身份头             │   │
│  │  4. 失败：通过AuthResponseWriter输出结构化响应         │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐   │
│  │ AuthPrincipal │  │ AuthCenter    │  │ AuthResponse  │   │
│  │               │  │ Client        │  │ Writer        │   │
│  │ • userId      │  │               │  │               │   │
│  │ • userName    │  │ • validate()  │  │ • writeError()│   │
│  │ • tenantId    │  │ • introspect()│  │               │   │
│  │ • roles       │  │               │  │               │   │
│  │ • scopes      │  └───────────────┘  └───────────────┘   │
│  └───────────────┘                                         │
│                                                             │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐   │
│  │ AuthHeader    │  │ AuthError     │  │ CachedAuth    │   │
│  │ Enricher      │  │ Code          │  │ CenterClient  │   │
│  │               │  │               │  │               │   │
│  │ • 清理伪造头   │  │ • AUTH_       │  │ • Token缓存   │   │
│  │ • 注入身份头   │  │   REQUIRED    │  │ • TTL控制     │   │
│  │               │  │ • INVALID_    │  │ • 并发控制    │   │
│  │               │  │   TOKEN       │  │               │   │
│  │               │  │ • TOKEN_      │  │               │   │
│  │               │  │   EXPIRED     │  │               │   │
│  │               │  │ • AUTH_       │  │               │   │
│  │               │  │   SERVICE_    │  │               │   │
│  │               │  │   UNAVAILABLE │  │               │   │
│  └───────────────┘  └───────────────┘  └───────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

#### 5.3.2 核心类设计

**AuthPrincipal - 认证主体**
```java
public class AuthPrincipal {
    private String userId;
    private String userName;
    private String tenantId;
    private List<String> roles;
    private List<String> scopes;
    private String clientId;
    private Instant expiresAt;
}
```

**AuthErrorCode - 错误码枚举**
```java
public enum AuthErrorCode {
    AUTH_REQUIRED,           // 未提供Token
    INVALID_TOKEN,           // Token无效
    TOKEN_EXPIRED,           // Token过期
    AUTH_SERVICE_UNAVAILABLE // 鉴权中心不可用
}
```

**AuthFilter - 核心过滤器**
```java
@Component
public class AuthFilter implements GlobalFilter, Ordered {
    
    @Autowired
    private AuthCenterClient authCenterClient;
    
    @Autowired
    private AuthHeaderEnricher authHeaderEnricher;
    
    @Autowired
    private AuthResponseWriter authResponseWriter;
    
    @Override
    public Mono<Void> filter(ServerWebExchange exchange, GatewayFilterChain chain) {
        return extractBearerToken(exchange)
            .switchIfEmpty(Mono.error(new AuthException(AuthErrorCode.AUTH_REQUIRED)))
            .flatMap(authCenterClient::validate)
            .flatMap(principal -> {
                ServerWebExchange mutated = authHeaderEnricher.enrich(exchange, principal);
                return chain.filter(mutated);
            })
            .onErrorResume(AuthException.class, ex -> 
                authResponseWriter.writeAuthError(exchange, ex));
    }
}
```

#### 5.3.3 身份透传头设计

| Header | 说明 | 示例值 |
|--------|------|--------|
| X-User-Id | 用户ID | u12345 |
| X-User-Name | 用户名 | zhangsan |
| X-Tenant-Id | 租户ID | t001 |
| X-User-Roles | 用户角色列表 | admin,mcp_user |
| X-User-Scopes | 用户权限范围 | mcp.invoke,mcp.read |
| X-Auth-Source | 认证来源 | mcp-gateway |

**安全要求**：网关必须先清理外部传入的同名Header，防止伪造。

### 5.4 错误码设计

#### 5.4.1 错误响应格式

**未提供Token (401)**
```json
{
  "error": "auth_required",
  "message": "Authentication required",
  "trace_id": "8c1f7d2a4d0f4f3b"
}
```

**Token过期 (401)**
```json
{
  "error": "token_expired",
  "message": "Access token expired",
  "trace_id": "15aa21f0aa9b4c7f"
}
```

**Token无效 (401)**
```json
{
  "error": "invalid_token",
  "message": "Access token is invalid",
  "trace_id": "2c5a8d01dcfa4f01"
}
```

**鉴权中心不可用 (503)**
```json
{
  "error": "auth_service_unavailable",
  "message": "Authentication service unavailable",
  "trace_id": "db0f7eaa52e84aa0"
}
```

### 5.5 时序图

#### 5.5.1 首次访问（无Token）

```
┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐
│智能体   │     │ Proxy  │     │ 浏览器  │     │鉴权中心 │     │ Gateway│     │ MCP服务│
└───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘
    │              │              │              │              │              │
    │ POST /mcp    │              │              │              │              │
    │─────────────▶│              │              │              │              │
    │              │              │              │              │              │
    │              │ 检查Token    │              │              │              │
    │              │ (不存在)     │              │              │              │
    │              │              │              │              │              │
    │              │ 打开浏览器   │              │              │              │
    │              │ (PKCE授权)   │              │              │              │
    │              │─────────────▶│              │              │              │
    │              │              │              │              │              │
    │ 返回提示:    │              │              │              │              │
    │ 请完成登录   │              │ 用户登录授权 │              │              │
    │◀─────────────│              │─────────────▶│              │              │
    │              │              │              │              │              │
    │              │              │ 302回调      │              │              │
    │              │              │◀─────────────│              │              │
    │              │              │              │              │              │
    │              │ GET /auth/callback          │              │              │
    │              │◀─────────────│              │              │              │
    │              │              │              │              │              │
    │              │ POST /token  │              │              │              │
    │              │─────────────────────────────▶              │              │
    │              │              │              │              │              │
    │              │ access_token + refresh_token│              │              │
    │              │◀─────────────────────────────              │              │
    │              │              │              │              │              │
    │              │ 安全保存Token│              │              │              │
    │              │              │              │              │              │
    │ POST /mcp    │              │              │              │              │
    │ (再次请求)   │              │              │              │              │
    │─────────────▶│              │              │              │              │
    │              │              │              │              │              │
    │              │ POST /mcp + Authorization   │              │              │
    │              │────────────────────────────────────────────▶              │
    │              │              │              │              │              │
    │              │              │              │ introspect   │              │
    │              │              │              │◀─────────────│              │
    │              │              │              │              │              │
    │              │              │              │ active=true  │              │
    │              │              │              │ + user info  │              │
    │              │              │              │─────────────▶│              │
    │              │              │              │              │              │
    │              │              │              │              │ 转发+身份头  │
    │              │              │              │              │─────────────▶│
    │              │              │              │              │              │
    │              │              │              │              │     响应     │
    │              │              │              │              │◀─────────────│
    │              │              │              │              │              │
    │     响应     │              │              │              │              │
    │◀─────────────│◀────────────────────────────────────────────              │
    │              │              │              │              │              │
```

#### 5.5.2 Token过期自动刷新

```
┌────────┐     ┌────────┐     ┌────────┐     ┌────────┐
│智能体   │     │ Proxy  │     │ Gateway│     │鉴权中心 │
└───┬────┘     └───┬────┘     └───┬────┘     └───┬────┘
    │              │              │              │
    │ POST /mcp    │              │              │
    │─────────────▶│              │              │
    │              │              │              │
    │              │ POST /mcp + 旧Token         │
    │              │─────────────▶│              │
    │              │              │              │
    │              │              │ introspect   │
    │              │              │─────────────▶│
    │              │              │              │
    │              │              │   expired    │
    │              │              │◀─────────────│
    │              │              │              │
    │              │ 401 token_expired           │
    │              │◀─────────────│              │
    │              │              │              │
    │              │ POST /token (refresh_token) │
    │              │─────────────────────────────▶
    │              │              │              │
    │              │ 新access_token              │
    │              │◀─────────────────────────────
    │              │              │              │
    │              │ 更新Token    │              │
    │              │              │              │
    │              │ 自动重放请求 │              │
    │              │─────────────▶│              │
    │              │              │              │
    │              │              │ introspect   │
    │              │              │─────────────▶│
    │              │              │              │
    │              │              │ active=true  │
    │              │              │◀─────────────│
    │              │              │              │
    │     200      │     200      │              │
    │◀─────────────│◀─────────────│              │
    │              │              │              │
```

---

## 6. 公司鉴权中心对接要求

### 6.1 需要提供的能力

#### 6.1.1 授权端点

```
GET /oauth2/authorize
```

**职责**：
- 展示登录页
- 用户登录并授权
- 回调本地代理

**参数**：
| 参数 | 说明 | 示例 |
|------|------|------|
| response_type | 固定值 | code |
| client_id | 客户端ID | local-mcp-proxy |
| redirect_uri | 回调地址 | http://127.0.0.1:8765/auth/callback |
| scope | 权限范围 | mcp.invoke mcp.read |
| code_challenge | PKCE挑战码 | (S256计算值) |
| code_challenge_method | 挑战方法 | S256 |
| state | 状态参数 | (随机字符串) |

#### 6.1.2 Token端点

```
POST /oauth2/token
```

**支持的授权类型**：
- `authorization_code`：授权码换Token
- `refresh_token`：刷新Token

**请求示例**：
```http
POST /oauth2/token
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code=xxx
&code_verifier=yyy
&redirect_uri=http://127.0.0.1:8765/auth/callback
&client_id=local-mcp-proxy
```

**响应示例**：
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "dGhpcyBpcyBhIHJlZnJlc2g...",
  "token_type": "Bearer",
  "expires_in": 300,
  "scope": "mcp.invoke mcp.read"
}
```

#### 6.1.3 Token校验端点

```
POST /oauth2/introspect
```

**请求示例**：
```http
POST /oauth2/introspect
Authorization: Bearer <gateway-service-token>
Content-Type: application/json

{
  "token": "<user-access-token>"
}
```

**响应示例**：
```json
{
  "active": true,
  "user_id": "u12345",
  "user_name": "zhangsan",
  "tenant_id": "t001",
  "roles": ["mcp_user", "ops_reader"],
  "scopes": ["mcp.invoke", "mcp.read"],
  "exp": 1777777777,
  "client_id": "local-mcp-proxy"
}
```

### 6.2 客户端注册要求

需要为本地代理注册`client_id`，并配置：

1. **允许的回调地址**：
   - `http://127.0.0.1:8765/auth/callback`
   - 或支持某范围端口的loopback callback

2. **允许申请的scope**：
   - `mcp.invoke`：调用MCP工具
   - `mcp.read`：读取MCP资源
   - `mcp.admin`：管理权限（可选）

---

## 7. 安全设计

### 7.1 安全要求清单

| 序号 | 要求 | 说明 |
|------|------|------|
| 1 | 使用Authorization Code + PKCE | 防止授权码被拦截 |
| 2 | 回调参数使用code | 不直接回传access_token |
| 3 | 严格校验state | 防止CSRF攻击 |
| 4 | 本地回调仅监听127.0.0.1 | 防止远程访问 |
| 5 | Token不允许明文落盘 | 使用系统安全存储 |
| 6 | 审计日志不记录完整Token | 脱敏处理 |
| 7 | 网关清理外部X-User-*头 | 防止身份伪造 |
| 8 | 网关与鉴权中心走可信网络 | 内网通信 |
| 9 | Refresh Token支持撤销 | 安全退出 |
| 10 | Token校验结果缓存 | 防止重放攻击 |

### 7.2 Token安全存储

| 平台 | 存储方式 |
|------|----------|
| Windows | Credential Manager (DPAPI加密) |
| macOS | Keychain |
| Linux | Secret Service / libsecret |

### 7.3 网关安全配置

```yaml
auth:
  auth-center-url: ${AUTH_CENTER_URL}
  validate-endpoint: /oauth2/introspect
  token-header-name: Authorization
  token-prefix: "Bearer "
  connect-timeout: 3000
  read-timeout: 3000
  cache-enabled: true
  cache-ttl-seconds: 60
  client-id: mcp-gateway
  service-token: ${AUTH_SERVICE_TOKEN}
```

---

## 8. 实施计划

### 8.1 阶段划分

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           实施阶段规划                                    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  P0阶段：链路打通 (2周)                                                   │
│  ├── 本地代理能登录                                                       │
│  ├── 本地代理能保存Token                                                  │
│  ├── 网关能校验Token                                                      │
│  └── 网关能把请求转发给下游MCP服务                                         │
│                                                                         │
│  P1阶段：可用性提升 (2周)                                                 │
│  ├── Token自动刷新                                                       │
│  ├── 网关结构化错误码                                                     │
│  ├── 鉴权中心不可用时返回503                                              │
│  └── 增加短期缓存                                                        │
│                                                                         │
│  P2阶段：服务MCP化 (4周)                                                  │
│  ├── 开发MCP SDK                                                         │
│  ├── 改造首批Java服务                                                    │
│  ├── 集成权限控制                                                        │
│  └── 联调测试                                                            │
│                                                                         │
│  P3阶段：治理增强 (持续)                                                   │
│  ├── 审计日志                                                            │
│  ├── 指标与告警                                                          │
│  └── Tool级别权限控制                                                    │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 8.2 任务拆解

#### A. 第三方智能体接入任务

| 任务 | 说明 | 负责方 |
|------|------|--------|
| A1 | 配置MCP地址为本地代理 | 智能体团队 |
| A2 | 验证HTTP MCP支持 | 智能体团队 |
| A3 | 验证认证失败提示展示 | 智能体团队 |
| A4 | 联调验证 | 双方 |

#### B. 本地MCP Auth Proxy开发任务

| 任务 | 说明 |
|------|------|
| B1 | 服务骨架：HTTP服务、接口暴露 |
| B2 | OAuth2/PKCE能力：state、code_verifier、code_challenge生成 |
| B3 | Token管理：存储、过期判断、刷新 |
| B4 | 请求转发：透传、注入Authorization、处理401 |
| B5 | 并发与稳定性：防重复登录、防重复刷新 |
| B6 | 可运维：日志、状态查询、手动登出 |

#### C. 鉴权中心开发任务

| 任务 | 说明 | 负责方 |
|------|------|--------|
| C1 | OAuth2能力补齐：PKCE支持 | 鉴权团队 |
| C2 | Token校验接口：返回用户身份 | 鉴权团队 |
| C3 | 客户端注册：client_id、回调白名单 | 鉴权团队 |

#### D. MCP Gateway开发任务

| 任务 | 说明 |
|------|------|
| D1 | 鉴权过滤器重构：主体校验、身份提取 |
| D2 | 结构化错误码：auth_required、invalid_token等 |
| D3 | 身份透传：清理伪造头、注入认证头 |
| D4 | 缓存与性能：Token校验缓存、超时配置 |
| D5 | 监控与审计：指标、日志、Trace ID |

#### E. Java服务MCP化任务

| 任务 | 说明 |
|------|------|
| E1 | MCP SDK开发：注解、协议解析、传输层 |
| E2 | 首批服务改造：用户服务、订单服务等 |
| E3 | 权限集成：读取身份头、Tool级别授权 |
| E4 | 测试验证：单元测试、集成测试 |

#### F. 测试任务

| 任务 | 说明 |
|------|------|
| F1 | 单元测试：Token解析、错误码映射 |
| F2 | 集成测试：各种认证场景 |
| F3 | 联调测试：端到端流程验证 |
| F4 | 性能测试：并发、缓存效果 |

### 8.3 里程碑

| 里程碑 | 时间 | 交付物 |
|--------|------|--------|
| M1 | 第2周末 | 本地代理+网关基础能力，链路打通 |
| M2 | 第4周末 | Token刷新、错误码、缓存，可用性达标 |
| M3 | 第8周末 | MCP SDK、首批服务改造完成 |
| M4 | 第10周末 | 审计、监控、权限控制完善 |

---

## 9. 监控与运维

### 9.1 监控指标

| 指标 | 说明 | 告警阈值 |
|------|------|----------|
| 鉴权成功率 | Token校验成功/总请求 | < 95% |
| 鉴权延迟 | Token校验P99延迟 | > 500ms |
| 鉴权中心可用性 | introspect接口成功率 | < 99% |
| Token刷新成功率 | refresh_token刷新成功/总刷新 | < 90% |
| MCP请求成功率 | 下游服务调用成功/总请求 | < 99% |

### 9.2 审计日志

记录以下信息：

| 字段 | 说明 |
|------|------|
| timestamp | 请求时间 |
| user_id | 用户ID |
| tenant_id | 租户ID |
| client_id | 客户端标识 |
| mcp_path | MCP路径 |
| tool_name | 工具名 |
| response_status | 响应状态 |
| duration | 耗时 |
| trace_id | 追踪ID |

---

## 10. 风险与应对

| 风险 | 影响 | 应对措施 |
|------|------|----------|
| 鉴权中心不可用 | 无法认证，服务不可用 | 缓存Token校验结果；降级策略 |
| Token刷新失败 | 用户需要重新登录 | 自动重试；友好提示 |
| 并发登录冲突 | 多个请求同时触发登录 | 登录状态锁；单次登录 |
| 本地端口冲突 | 代理无法启动 | 端口自动选择；错误提示 |
| 服务改造工作量大 | 延期风险 | 优先核心服务；SDK简化改造 |

---

## 11. 总结

本方案通过引入**本地MCP Auth Proxy**作为认证代理，实现了：

1. **对第三方智能体透明**：智能体无需理解公司认证协议
2. **统一鉴权入口**：MCP Gateway作为统一鉴权网关
3. **身份安全透传**：网关校验后安全注入身份信息
4. **最小化改造**：Java服务通过SDK快速MCP化
5. **可扩展架构**：支持后续Tool级别授权、审计、限流

通过本方案的实施，可以在**不改造第三方智能体内部实现**的前提下，完成企业级身份接入，并保留后续权限治理和安全审计的扩展空间。
