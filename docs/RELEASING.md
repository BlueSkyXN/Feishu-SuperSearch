# 发布指南

## 1. 交付原则

SuperFeishuSearch 的正式发布由 GitHub Actions 构建和验证。维护者本机的 `bin/`、`dist/` 或手工 ZIP 不是用户交付物。

```text
PR exact head green
→ merge main
→ main exact head green
→ vX.Y.Z tag points to that main commit
→ Release workflow green
→ GitHub Release 9 assets readback
```

## 2. 版本同步

正式版本只支持 `MAJOR.MINOR.PATCH`。必须同步：

```text
cmd/sfs/main.go
Makefile
Dockerfile
api/openapi.yaml
web/package.json
web/package-lock.json
CHANGELOG.md
docs/IMPLEMENTATION_STATUS.md
```

检查：

```bash
./scripts/check-version.sh 1.0.2
```

## 3. PR 门禁

发布 PR 必须至少获得以下 exact-head 终态：

```text
CI / Verify
CI / Web
CI / Platform / linux-native
CI / Platform / macos-native
CI / Platform / windows-native
CI / Docker image
Security / Gitleaks
Security / NPM audit
Security / Govulncheck
Security / CodeQL
```

PR 合并后，必须等待同一 merge SHA 在 `main` 的 CI 和 Security 再次完成。PR merge ref 或上一个 main SHA 的绿色结果不能替代。

## 4. 创建 Tag

确认本地和远端 main 一致：

```bash
git fetch origin main
git switch main
git pull --ff-only origin main
git rev-parse HEAD
```

创建并推送 Tag：

```bash
git tag -a v1.0.2 -m "SuperFeishuSearch 1.0.2"
git push origin v1.0.2
```

Release workflow 会重新验证 Tag commit 等于触发发布时的 `origin/main` head。Tag 指向旧的 main 历史提交、版本不匹配、测试失败、资产不完整或同名 Release 已存在时，发布直接失败。

## 5. Release workflow

`package` Job：

1. 固定 Tag commit 和稳定版本；
2. `make verify`；
3. `npm ci` 与 High/Critical audit；
4. Web unit/typecheck/build/static drift；
5. Playwright 离线完整预览 E2E；
6. 65%/75% coverage gate；
7. HTTP/Web smoke；
8. `make package-only`；
9. `make release-verify`，并在无 `.git` 的临时目录中物化源码 ZIP、重新执行完整 `make verify`；
10. 上传含 run ID/attempt 的不可变 Actions artifact。

`publish` Job 只有 `contents: write`。它下载已验证 bundle，以 `ARCHIVE_EXECUTION=skip` 再次执行被动 `make release-verify`，不会运行归档内的二进制或源码；复核通过后才用 `gh release create --verify-tag` 创建 Release。

## 6. 精确资产集合

GitHub Release 必须恰好包含：

```text
SuperFeishuSearch-1.0.2-linux-amd64.tar.gz
SuperFeishuSearch-1.0.2-linux-arm64.tar.gz
SuperFeishuSearch-1.0.2-darwin-amd64.tar.gz
SuperFeishuSearch-1.0.2-darwin-arm64.tar.gz
SuperFeishuSearch-1.0.2-windows-amd64.zip
SuperFeishuSearch-1.0.2-windows-arm64.zip
SuperFeishuSearch-1.0.2-source.tar.gz
SuperFeishuSearch-1.0.2-source.zip
SHA256SUMS
```

`scripts/verify-release.py` 检查压缩包安全路径、无 symlink、运行文档和 `config.demo.json`、仓库完整 `LICENSES/` 与逐文件 SHA-256、六平台 build metadata、完整 commit、Linux amd64 Demo smoke、源码 ZIP/TAR 文件内容和模式一致性、与 Git HEAD 全部 tracked blob 的内容和模式一致性，以及敏感路径排除。随后它会安全物化源码 ZIP、恢复可执行位，并在没有 `.git` 元数据的目录中执行完整 `make verify`；任何失败都会阻止 Actions artifact 和 GitHub Release。

## 7. 发布后远程回读

```bash
gh run list --workflow release.yml --limit 10
gh release view v1.0.2 --json tagName,url,assets
tmp="$(mktemp -d)"
gh release download v1.0.2 --dir "$tmp"
(cd "$tmp" && sha256sum -c SHA256SUMS)
```

还要确认：

- Tag commit 等于目标 main SHA；
- Release workflow 的 `headSha` 等于该 SHA；
- 资产数为 9；
- Linux amd64 下载包能运行 `sfs version`；
- `config.demo.json` 能启动 Web，并完成 Search、Research、Ask、Evidence/Citation、Preview、Doctor、Session；
- Release 页面没有被旧资产覆盖。

## 8. 真实租户验收

真实飞书不放在普通 PR/Release runner。通过 `Live Smoke` 手动工作流，使用：

```text
main ref
live-readonly Environment
required reviewer
self-hosted,sfs-live runner
只读 profile/token
```

必须把 run SHA、时间和脱敏来源状态记录为验收证据。缺 scope 的来源应保留 `missing_scope`；Base/Sheets 缺测试对象时应保留阻塞，不得写成通过。Mock、fixture、HTTP fake 或 GitHub Release 都不能替代租户验收。

## 9. 手动候选打包

`Release` workflow 的 `workflow_dispatch` 可以验证某个稳定版本并上传 Actions artifact，但不会创建 GitHub Release。它适合预检，不是正式发布。

维护者确有需要时可在干净 Git checkout 执行：

```bash
make package VERSION=1.0.2
make release-verify VERSION=1.0.2 COMMIT="$(git rev-parse HEAD)"
```

打包脚本要求 clean worktree，并用 `git archive HEAD` 生成 source archive。普通用户无需运行这些命令。

## 10. 回滚

- 不移动或覆盖已发布 Tag；
- 不替换旧 Release 资产；
- 保留上一版本下载；
- 配置或数据库变更前备份 `~/.sfs/sfs.db`；
- 发布新的 PATCH 版本修复问题；
- 真实 Provider 漂移时可暂时固定已验证的 `lark-cli` 版本，或使用 replay 复现，不得静默回退 Mock。
