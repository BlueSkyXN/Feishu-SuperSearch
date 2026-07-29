# Third-Party Notices

SuperFeishuSearch 整体以 GPL-3.0 分发。初始实现材料由
SuperFeishuSearch contributors 按 MIT License 提供，原始通知保存在
[`LICENSES/MIT-SuperFeishuSearch-materials.txt`](LICENSES/MIT-SuperFeishuSearch-materials.txt)。

正式二进制会静态链接 Go 运行时依赖，并内嵌 Web 生产依赖。完整、机器可校验的
版本与许可证文件映射保存在
[`LICENSES/runtime-dependencies.json`](LICENSES/runtime-dependencies.json)；
`scripts/check-third-party-licenses.py` 会在依赖集合、版本、许可证文件或 SHA-256
发生未同步变化时失败。Release 运行包会携带整个 `LICENSES/` 目录。

## Go runtime modules

| Component | Version | License | Notice |
|---|---|---|---|
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT | `LICENSES/runtime/go-dustin-go-humanize.txt` |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause | `LICENSES/runtime/go-google-uuid.txt` |
| `github.com/larksuite/oapi-sdk-go/v3` | `v3.9.9` | MIT | `LICENSES/MIT-larksuite-oapi-sdk-go.txt` |
| `github.com/mattn/go-isatty` | `v0.0.20` | MIT | `LICENSES/runtime/go-mattn-go-isatty.txt` |
| `github.com/ncruces/go-strftime` | `v0.1.9` | MIT | `LICENSES/runtime/go-ncruces-go-strftime.txt` |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | BSD-3-Clause | `LICENSES/runtime/go-remyoudompheng-bigfft.txt` |
| `github.com/santhosh-tekuri/jsonschema/v6` | `v6.0.2` | Apache-2.0 | `LICENSES/runtime/go-jsonschema-v6-Apache-2.0.txt` |
| `golang.org/x/exp` | `v0.0.0-20250620022241-b7579e27df2b` | BSD-3-Clause | `LICENSES/runtime/go-golang-x-exp.txt` |
| `golang.org/x/sys` | `v0.34.0` | BSD-3-Clause | `LICENSES/runtime/go-golang-x-sys.txt` |
| `golang.org/x/text` | `v0.14.0` | BSD-3-Clause | `LICENSES/runtime/go-golang-x-text.txt` |
| `modernc.org/libc` | `v1.66.3` | BSD-3-Clause AND MIT | `LICENSES/runtime/go-modernc-libc*.txt` |
| `honnef.co/go/netdb`（由 `modernc.org/libc` 内嵌） | bundled in `v1.66.3` | MIT | `LICENSES/runtime/go-modernc-libc-honnef-netdb.txt` |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause | `LICENSES/runtime/go-modernc-mathutil.txt` |
| `modernc.org/memory` | `v1.11.0` | BSD-3-Clause | `LICENSES/runtime/go-modernc-memory*.txt` |
| `modernc.org/sqlite` | `v1.38.2` | BSD-3-Clause | `LICENSES/runtime/go-modernc-sqlite.txt` |

## Embedded Web production packages

| Component | Version | License | Notice |
|---|---|---|---|
| `react` | `18.3.1` | MIT | `LICENSES/runtime/npm-react-family.txt` |
| `react-dom` | `18.3.1` | MIT | `LICENSES/runtime/npm-react-family.txt` |
| `scheduler` | `0.23.2` | MIT | `LICENSES/runtime/npm-react-family.txt` |
| `loose-envify` | `1.4.0` | MIT | `LICENSES/runtime/npm-loose-envify.txt` |
| `js-tokens` | `4.0.0` | MIT | `LICENSES/runtime/npm-js-tokens.txt` |

各许可证原文中的版权声明、分发条件和免责声明保持不变。该清单只覆盖正式运行物；
开发与测试依赖不进入发布二进制或内嵌 Web 静态资源。
