# 快速开始

## 1. 下载，不需要本地编译

打开 [GitHub Releases](https://github.com/BlueSkyXN/Feishu-SuperSearch/releases/latest)，下载当前系统对应的压缩包和 `SHA256SUMS`。

| 系统 | 文件名 |
|---|---|
| macOS Apple Silicon | `SuperFeishuSearch-1.0.2-darwin-arm64.tar.gz` |
| macOS Intel | `SuperFeishuSearch-1.0.2-darwin-amd64.tar.gz` |
| Linux x86-64 | `SuperFeishuSearch-1.0.2-linux-amd64.tar.gz` |
| Linux ARM64 | `SuperFeishuSearch-1.0.2-linux-arm64.tar.gz` |
| Windows x86-64 | `SuperFeishuSearch-1.0.2-windows-amd64.zip` |
| Windows ARM64 | `SuperFeishuSearch-1.0.2-windows-arm64.zip` |

macOS/Linux：

```bash
tar -xzf SuperFeishuSearch-1.0.2-<os>-<arch>.tar.gz
cd SuperFeishuSearch-1.0.2-<os>-<arch>
./sfs version
```

Windows PowerShell：

```powershell
Expand-Archive .\SuperFeishuSearch-1.0.2-windows-amd64.zip
cd .\SuperFeishuSearch-1.0.2-windows-amd64
.\sfs.exe version
```

发布包包含二进制、离线 Demo 配置、运行文档、完整第三方许可证和 Agent SKILL。运行二进制不需要系统 SQLite、CGO、Go 或 Node.js。

## 2. 预览全部功能

在解压目录运行：

```bash
./sfs --config config.demo.json serve
```

Windows 使用：

```powershell
.\sfs.exe --config config.demo.json serve
```

打开 [http://127.0.0.1:3765](http://127.0.0.1:3765)。无需飞书账号或 AI Key，即可操作：

- 多来源 Search 和来源状态；
- Continue 加载下一页；
- Research、Artifact 和 Evidence；
- Ask、Claim 和 Citation；
- 候选对象正文预览；
- Doctor 与 Provider 能力；
- Session 历史；
- Session 恢复；
- 桌面/移动端、浅色/深色布局。

离线 Ask 使用确定性 Demo Answerer，只引用 Mock Evidence，并明确标注 Demo。它不访问网络，也不代表真实模型或真实租户结果。

## 3. CLI 离线验证

```bash
./sfs --backend mock search "A 项目 延期" \
  --sources docs,messages,minutes,meetings,tasks

./sfs --backend mock research "A 项目为什么延期，还有哪些任务未完成" \
  --sources docs,messages,minutes,tasks \
  --fetch-top 6

./sfs --config config.demo.json ask "A 项目为什么延期" \
  --sources docs,messages,minutes
```

JSON 输出：

```bash
./sfs --backend mock --output json \
  search "A 项目 延期" --sources docs,messages
```

## 4. 接入真实 lark-cli

真实模式仍是本地单用户、只读模式。先确认 `lark-cli` 已登录：

```bash
lark-cli --version
lark-cli auth status
```

探测 Provider：

```bash
./sfs --backend larkcli --profile work --as user doctor
```

最小查询：

```bash
./sfs --backend larkcli --profile work --as user \
  search "一个确认存在的关键词" \
  --sources docs,messages,minutes
```

SuperFeishuSearch 不复制 `lark-cli` credential。它将 profile 和 identity 作为受控参数传给子进程；来源缺 scope 时返回 `missing_scope`，不会静默切换到 Mock。

## 5. 接入 Direct OpenAPI

Direct OpenAPI 覆盖 docs/messages/people/minutes 的高频只读操作。官方 SDK 需要 App ID 和 user access token：

```bash
export SFS_OPENAPI_APP_ID='cli_xxx'
export FEISHU_USER_ACCESS_TOKEN='u-...'
./sfs --backend openapi --as user search "周报" --sources docs
```

Token 只从环境变量读取。不要把 Token 写入配置、命令参数、Git、日志或 fixture。Operation 级混合路由见 [配置参考](CONFIGURATION.md)。

## 6. Session

真实安装默认使用纯 Go SQLite：

```text
~/.sfs/sfs.db
```

旧 JSON Session 可幂等导入，成功前不会删除源文件：

```bash
./sfs migrate sessions --from ~/.sfs/sessions --to ~/.sfs/sfs.db
```

Session、Continue、Fetch 和 Expand 都绑定创建它们的 identity scope；另一个 profile 不能读取、继续或删除该 Session。

## 7. 启动 MCP

```bash
./sfs --backend mock mcp
```

MCP 使用 stdio。宿主应把 `sfs` 作为子进程启动，不要把诊断文本写到 MCP stdout。工具和参数见 [MCP 指南](MCP_GUIDE.md)。

## 8. 安全边界

- HTTP/Web 强制回环监听；
- Host 必须是 `localhost` 或回环 IP；
- 浏览器 Origin 必须与服务端同源，不接受任意本机端口；
- v1 没有多人认证或公网部署能力；
- AI 默认关闭；启用远程 AI 会发送查询、候选摘要或 Evidence 到配置的模型服务；
- 真实飞书访问始终受原 profile/token 的 scope 与对象权限限制。

详见 [安全模型](SECURITY_MODEL.md) 和 [故障排查](TROUBLESHOOTING.md)。

## 9. 源码贡献者

只有开发和维护发布流程时才需要本地工具链：Go 1.23+、Node.js 22 和 Playwright Chromium。

```bash
git clone https://github.com/BlueSkyXN/Feishu-SuperSearch.git
cd Feishu-SuperSearch
make verify
make coverage
make web-install
make web-test
make http-smoke
```

正式跨平台包由 GitHub Actions 生成。普通用户不要本地执行 `make package`。
