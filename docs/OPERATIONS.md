# 运行与部署

## 1. 运行模式

### 个人本地 CLI

推荐：

```text
backend = larkcli
storage.type = sqlite
storage.path = ~/.sfs/sfs.db
HTTP 不启动
```

优点是直接复用本机用户 OAuth 和可见范围。

### 本地 HTTP/Web

```bash
sfs --backend larkcli --profile work --as user \
  serve --listen 127.0.0.1:3765
```

适合浏览器、同机 Agent 和本地脚本。

### 容器

默认镜像输出版本；适合执行 CLI、离线验证或作为受控本机进程使用：

```bash
docker build -t superfeishusearch:local .
docker run --rm superfeishusearch:local
docker run --rm superfeishusearch:local \
  --backend mock search "A 项目" --sources docs,messages
```

Direct OpenAPI CLI：

```bash
docker run --rm \
  -e SFS_BACKEND=openapi \
  -e SFS_OPENAPI_APP_ID \
  -e SFS_OPENAPI_TOKEN \
  superfeishusearch:local search "周报" --sources docs
```

镜像默认不包含 `lark-cli` 和本机 OAuth profile。v1 的 HTTP/Web 强制进程内回环监听，不能通过容器端口发布为远程服务；容器化多人服务属于 Server v2。不要把宿主 credential 烘焙进镜像。

## 2. 网络暴露

HTTP 服务没有内置多用户认证。默认只监听：

```text
127.0.0.1:3765
```

内置 `serve` 会拒绝 `0.0.0.0`、空主机和其他非回环监听地址。需要反向代理时，让代理连接 `127.0.0.1` 或 `::1` 上的 SFS，并由代理提供：

- TLS；
- 用户认证；
- 请求大小限制；
- 请求速率限制；
- 访问日志脱敏；
- SSE 关闭缓冲；
- 仅允许必要来源和身份。

浏览器跨域请求只允许来自 `localhost`、`127.0.0.1` 或 `::1`。不要直接将携带个人 user token 的实例暴露给多人共享。

## 3. Session 存储

### SQLite 默认模式

```json
{
  "storage": {
    "type": "sqlite",
    "path": "~/.sfs/sfs.db",
    "ttl": "45m"
  }
}
```

SQLite 工作集可能包含：

- 搜索 query；
- Candidate 标题与 snippet；
- Artifact 内容；
- 关系；
- Provider 错误；
- Cursor；
- identity scope key。

数据库和父目录权限应只允许当前服务账号访问。程序使用 WAL 与事务 migration；定期清理过期 Session，不要同步到公共云盘或代码仓库。

旧 JSON File Store 仍可用，使用 `sfs migrate sessions` 幂等导入 SQLite；导入不会删除源文件。

### 内存模式

适合临时服务、测试或不希望落盘的环境。进程退出后 Session 丢失，也不能跨副本共享。

## 4. 多副本

当前 SQLite/File/Memory Session 只面向本地单实例。多副本部署需要：

- Session sticky routing；或
- 实现共享 SessionStore；
- 保持 identity scope 隔离；
- 对 Fetch singleflight 只能保证单进程范围有明确预期。

项目当前没有内置分布式锁或共享数据库 Adapter。

## 5. 预算与并发

建议先从默认值开始：

```text
global_concurrency = 6
provider_timeout = 4s
balanced deadline = 8s
max_calls = 24
max_bytes = 32 MiB
```

当上游 429 增多时，优先降低并发和每来源页数，而不是增加 Retry 次数。

对长文档和消息上下文，应限制 `max_fetches` 和 Projection；不要默认读取全部正文。

## 6. lark-cli 进程

每次调用：

- 使用参数数组；
- 不经过 shell；
- 有 Context timeout；
- stdout/stderr 分离并限制大小；
- 进程结束后解析 envelope；
- 将 profile/identity 传给 CLI。

运维时应固定已验证的 lark-cli 版本，并通过 `doctor`、record/replay 检查升级后的字段漂移。

## 7. Direct OpenAPI Token

优先使用环境变量或 Secret Store 注入：

```bash
export SFS_OPENAPI_TOKEN='u-...'
export SFS_OPENAPI_APP_ID='cli_xxx'
```

避免：

- 写入镜像；
- 写入 Git；
- 打印到进程参数；
- 写入 fixture；
- 在错误响应中回显。

Token 轮换后重启进程，使配置重新加载。

## 8. 健康检查

轻量：

```bash
curl -f http://127.0.0.1:3765/v1/health
```

Provider 实际探测：

```bash
curl -sS 'http://127.0.0.1:3765/v1/capabilities?probe=true'
```

`probe=true` 可能启动外部进程或访问上游，不适合作为高频容器 liveness probe。

推荐：

```text
liveness  → /v1/health
readiness → 启动时或低频 capabilities probe
```

## 9. 日志与诊断

当前主要诊断来源：

- CLI stderr；
- HTTP ErrorDetail；
- SearchSnapshot.sources；
- Session events；
- Plan RetrievalEvent；
- Provider record/replay。

日志中不要写：

- Token；
- Authorization header；
- 完整邮件或消息正文；
- 未脱敏人员标识；
- 外部 Planner/Provider 的原始敏感 stderr。

## 10. 升级

升级步骤：

1. 阅读 `CHANGELOG.md`；
2. 在 Mock/Replay 环境运行 `make verify`；
3. 新二进制执行 `doctor`；
4. 对关键来源运行最小 Search；
5. 验证 Fetch Projection；
6. 再切换正式进程；
7. 保留上一版本二进制便于回滚。

Session Schema 当前以向后容忍为目标，但重大版本升级前仍建议清理旧 Session 或先在副本上验证。
