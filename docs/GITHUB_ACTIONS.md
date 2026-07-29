# GitHub Actions CI/CD

正式交付链是 GitHub 上的 commit、Actions run、Tag 和 Release，不是维护者机器上的 `dist/` 目录。

```text
feature branch
  → Pull Request
  → exact PR head CI + Security
  → merge main
  → exact main head CI + Security
  → protected vX.Y.Z tag
  → Release validation/package
  → 9 个 GitHub Release assets
```

任何 `pending`、`skipped`、`cancelled`、不同 SHA 或仅本地成功的结果，都不能写成 exact-head 通过。

## 1. CI

文件：[`.github/workflows/ci.yml`](../.github/workflows/ci.yml)

触发：

- 指向 `main` 的 Pull Request；
- push 到 `main`；
- merge queue 的 `merge_group`；
- 手动 `workflow_dispatch`。

不对普通 feature branch push 重复运行整套矩阵；打开 PR 后验证 PR head，合并后再验证 `main` exact head。

### `CI / Verify`

```text
gofmt
go vet ./...
go test ./...
go test -race ./...
整仓 coverage >= 65%
核心包 coverage >= 75%
文档链接、JSON、OpenAPI、Workflow、Shell/Python/Ruby 检查
actionlint v1.7.7（保持与项目 Go 1.23 基线兼容）
Mock CLI smoke
```

Coverage artifact 名包含 `run_id`、`run_attempt` 和 SHA，避免 rerun 与不可变 artifact 冲突。

### `CI / Web`

```text
Node.js 22
npm ci
Vitest
TypeScript typecheck
Vite production build
Go embed 静态资产漂移检查
Playwright Chromium E2E
```

Playwright 使用 `config.demo.json`，覆盖 Search、Continue、Research、离线 Ask、Claim/Citation、对象预览、Doctor、Session 恢复、390×844、深色模式和 axe 无障碍。失败时上传 `.tmp/playwright` trace/screenshot artifact。

### `CI / Platform / *`

GitHub-hosted Linux、macOS、Windows runner 原生运行 `go test ./...` 并构建二进制。Job 会：

- 回读真实 `GOOS/GOARCH` 与 `runner.arch`；
- 使用 `CGO_ENABLED=0`、`-trimpath` 和 `-buildvcs=false`；
- 注入当前 `GITHUB_SHA` 与 UTC 构建时间；
- 运行 `sfs version` 并断言 commit；
- 用 `go version -m` 回读平台、架构和 CGO。

预览 artifact 名含平台、真实 runner 架构、`run_id`、`run_attempt` 和 SHA。

### `CI / Docker image`

只在 Verify 和 Web 成功后运行。镜像嵌入当前 SHA，并在容器内执行 `version` 和 Mock Search。

## 2. Security

文件：[`.github/workflows/security.yml`](../.github/workflows/security.yml)

对每个指向 `main` 的 PR、`main` push、`merge_group`、每周定时和手动运行都产生明确终态，不使用会让 required check 永久停在 `Expected` 的 path filter。

Jobs：

```text
Security / Gitleaks
Security / NPM audit
Security / Govulncheck
Security / CodeQL
```

- Gitleaks 扫描提交历史和当前改动，许可证清单只精确豁免已由 manifest 校验的 SHA-256 行；
- NPM audit 使用锁文件干净安装并阻断 High/Critical；
- Govulncheck 扫描 Go 可达调用路径；
- CodeQL 使用 Go manual build 和 `security-and-quality` 查询集；
- 只有 CodeQL Job 获得 `security-events: write`。

## 3. Release

文件：[`.github/workflows/release.yml`](../.github/workflows/release.yml)

正式发布只接受稳定格式：

```text
vMAJOR.MINOR.PATCH
```

Tag workflow 首先校验：

- 版本与仓库八处声明一致；
- checkout commit 与 Tag 指向一致；
- Tag commit 等于触发发布时的 `origin/main` head；
- 完整 Verify、Web、Playwright、coverage、HTTP/Web smoke 全部通过。

随后 `make package-only` 生成 6 个运行包、2 个源码包和 `SHA256SUMS`。`make release-verify` 在发布前检查：

- `dist/release` 恰好 9 个文件；
- checksum 文件恰好覆盖 8 个压缩包并逐项回读；
- 六平台 `GOOS/GOARCH`、`CGO_ENABLED=0`、`trimpath`；
- 每个二进制嵌入当前 version 和完整 commit；
- 每个运行包包含 `config.demo.json`、运行文档、`THIRD_PARTY_NOTICES.md` 和完整 `LICENSES/`；
- `make verify` 会对六个发布目标的 Go 运行时模块并集、Web 生产依赖、版本和许可证 SHA-256 做闭合集合检查，新增依赖但未补 notice 时直接失败；
- Linux amd64 `version` 与离线 Demo Search 实际运行；
- source ZIP/TAR 的文件内容和可执行模式一致；
- source archive 与 Git HEAD 全部 tracked blob 的文件名、内容 SHA-256 和模式完全一致；
- `local/`、`dist/`、`bin/`、`.tmp/`、`minutes/`、`.env*` 等敏感/临时路径未进入源码包。
- source ZIP 被安全物化到无 `.git` 的临时目录，并重新执行完整 `make verify`；源码包本身不可独立验证时不发布。

`package` Job 只有 `contents: read`，负责实际运行 Linux amd64 Demo 与源码包 `make verify`。验证后的 bundle 作为不可变 Actions artifact 传给独立 `publish` Job；artifact 名由 `package` 输出给下游，单独重跑失败的 `publish` Job 也不会漂移到不存在的 `run_attempt` 名称。只有 `publish` 获得 `contents: write`，下载后以 `ARCHIVE_EXECUTION=skip` 运行同一 verifier 的被动校验路径，不执行归档内二进制或源码，再创建 GitHub Release。已存在的同名 Release 不会被覆盖。

手动 `workflow_dispatch` 只生成并验证候选 artifact，不创建 Release。

## 4. Live Smoke

文件：[`.github/workflows/live-smoke.yml`](../.github/workflows/live-smoke.yml)

这是一条受保护的真实租户只读验收，不是普通 CI：

- 只能手动触发；
- `main-guard` 明确拒绝非 `refs/heads/main`；
- 日志绑定当前 `GITHUB_SHA`；
- 使用 `self-hosted,sfs-live` 受控 runner；
- 绑定 `live-readonly` Environment；
- Environment 应限制为 `main` 并配置 required reviewer；
- 不上传真实响应、对象正文或凭据 artifact；
- 所有操作只读。

基础 secrets：

```text
SFS_LIVE_PROFILE
SFS_LIVE_QUERY
SFS_LIVE_PERSON_QUERY
```

Base/Sheets 和指定 Fetch/Expand 对象需要额外 `SFS_LIVE_*`，完整列表见 workflow。一个成功 run 只证明该 SHA、该时间、该租户和当时权限，不自动证明其他环境。

## 5. Dependabot

[`.github/dependabot.yml`](../.github/dependabot.yml) 覆盖：

- Go Modules：每周；
- NPM `/web`：每周；
- GitHub Actions：每周；
- Docker：每月。

## 6. Branch 与 Tag 保护

`main` Ruleset 建议要求：

```text
Pull Request
conversation resolution
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
禁止 force push
禁止删除
```

单维护者仓库不要配置会永久自锁的强制 approval 数量；是否要求 reviewer 是仓库治理决策。启用 merge queue 前必须保留 `merge_group` 触发。

`v*` Tag Ruleset 应限制创建者并禁止更新/删除。Release workflow 的 exact `origin/main` head 检查是第二层防线，不能替代 Tag 保护。

## 7. 如何核验交付

```bash
gh pr checks <PR_NUMBER>
gh run list --branch main --limit 20
gh release view v1.0.3 --json tagName,targetCommitish,url,assets
gh release download v1.0.3 --pattern SHA256SUMS
```

需要同时记录：PR head SHA、merge SHA、main workflow SHA、Tag SHA、Release URL、9 个资产名和校验和。只看到绿色 badge 或 Release 页面标题不够。
