# 安全策略

## 支持范围

当前维护分支：

| 版本 | 状态 |
|---|---|
| `1.0.x` | 支持安全修复 |
| `<1.0` | 不再支持 |

## 报告安全问题

请不要通过公开 Issue 报告以下问题：

- Access Token、OAuth profile 或凭证泄露；
- 不同用户/profile 之间的 Session 或对象缓存串用；
- 命令注入、路径穿越、任意文件读取；
- Provider 输出导致的内存或磁盘资源耗尽；
- 访问到当前飞书身份不可见的数据；
- HTTP/MCP 接口绕过身份或预算限制。

请使用 GitHub 仓库的 **Security → Report a vulnerability** 私密报告入口。报告中包含：

1. 受影响版本与平台；
2. 最小复现步骤；
3. 预期与实际行为；
4. 影响范围；
5. 已知缓解措施；
6. 必要时附脱敏日志。

## credential 处理

- `lark-cli` 模式不复制 OAuth profile，由子进程按当前 profile 执行；
- Direct OpenAPI Token 只能通过 `token_env` 指向的环境变量注入；
- 配置文件拒绝明文 `open_api.token`，不要把 Token 写入项目配置；
- 日志、fixture、错误信息和 `Provenance.RawRef` 不应包含 Token；
- Record/Replay fixture 在提交前必须脱敏。

## 部署边界

默认 HTTP 服务只监听 `127.0.0.1`，没有内置用户认证。将其暴露到局域网或公网前，必须放在具备身份认证、TLS、访问控制和请求大小限制的反向代理后面。

SuperFeishuSearch 继承上游飞书身份的可见范围，但它不是额外的权限系统。使用 bot/user 混合 Provider 时，需要明确每条路由使用的身份。
