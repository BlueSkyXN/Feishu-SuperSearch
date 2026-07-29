## 变更目标

<!-- 说明问题、使用场景和本次变更。 -->

## 设计与边界

<!-- 说明 Kernel / Adapter / Planner / Transport 中哪些层发生变化。 -->

## 测试

- [ ] `make verify`
- [ ] `make coverage`（整仓 >=65%，核心 >=75%）
- [ ] `make web-test`（Vitest、typecheck、build/static drift、Playwright）
- [ ] `make http-smoke`
- [ ] `npm --prefix web audit --audit-level=high`
- [ ] 增加或更新单元测试
- [ ] 更新相关文档、OpenAPI 或 JSON Schema
- [ ] 未提交 Token、真实消息/邮件正文或未脱敏 fixture

## 兼容性

<!-- 是否影响配置、Provider 输出、CLI/API/MCP 契约或 Session。 -->

## 真实飞书联调

<!-- 未执行时明确写“未执行”；执行时只描述脱敏结果。 -->

## GitHub 交付

- [ ] PR exact-head CI / Security 通过
- [ ] 离线 Demo Search / Research / Ask / Evidence / Citation 可预览
- [ ] Release 相关改动已说明 Tag、资产集合和回读方式
- [ ] 未把本机 `dist/`、`bin/` 或 ZIP 当作正式交付

## 剩余边界

<!-- 明确未执行的 live tenant、权限、Base/Sheets 对象、direct OpenAPI、Release 或部署验证。 -->
