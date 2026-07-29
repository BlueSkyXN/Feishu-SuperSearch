# SuperFeishuSearch 文档

这组文档按使用角色组织。首次使用建议依次阅读“快速开始 → 配置 → CLI 或 API”。

## 使用者

| 文档 | 内容 |
|---|---|
| [快速开始](GETTING_STARTED.md) | 安装、Mock 演示、真实飞书接入、第一次 Search/Fetch/Research |
| [配置参考](CONFIGURATION.md) | 配置优先级、Backend、Provider 路由、环境变量、Planner |
| [CLI 参考](CLI_REFERENCE.md) | 全局参数和所有子命令 |
| [HTTP API](HTTP_API.md) | REST、SSE、错误映射、curl 示例 |
| [MCP 指南](MCP_GUIDE.md) | MCP stdio 启动、工具列表、Agent 调用顺序 |
| [故障排查](TROUBLESHOOTING.md) | scope、identity、lark-cli、Session、分页、输出解析 |
| [运行与部署](OPERATIONS.md) | 本地服务、Docker、反向代理、Session 和日志边界 |

## 架构与扩展

| 文档 | 内容 |
|---|---|
| [架构说明](ARCHITECTURE.md) | Kernel、Provider、Planner、Session、排序与执行图 |
| [RetrievalPlan](RETRIEVAL_PLAN.md) | `retrieval-plan/v1`、节点、依赖、预算、事件 |
| [Provider 开发](PROVIDER_GUIDE.md) | Go Provider 与外部 JSON-RPC Provider |
| [lark-cli 兼容策略](LARKCLI_COMPATIBILITY.md) | 命令映射、版本漂移、身份与 scope |
| [安全模型](SECURITY_MODEL.md) | credential、Session 隔离、子进程、HTTP 暴露边界 |
| [完整技术设计](SuperFeishuSearch_完整技术设计_v1.md) | 原始完整版设计与决策背景 |

## 开发与发布

| 文档 | 内容 |
|---|---|
| [开发指南](DEVELOPMENT.md) | 目录、依赖方向、常见改动路径、调试方法 |
| [测试指南](TESTING.md) | 单元、集成、Mock、Replay、HTTP、MCP 和真实租户测试 |
| [GitHub Actions](GITHUB_ACTIONS.md) | CI、安全扫描、发布工作流和分支保护 |
| [发布指南](RELEASING.md) | 版本、Changelog、打包、Tag 与 GitHub Release |
| [实现状态](IMPLEMENTATION_STATUS.md) | 当前实现范围与未能替代的真实租户验证 |

仓库级协作规则见 [贡献指南](../CONTRIBUTING.md) 与 [安全策略](../SECURITY.md)。
