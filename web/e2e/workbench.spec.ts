import { expect, test } from "@playwright/test";
import axe from "axe-core";

test("桌面端完成搜索、预览、Doctor 与 Session 恢复", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto("/");
  await expect(page.getByText("仅限本机回环访问")).toBeVisible();
  await expect(page.getByText(/mock · memory/)).toBeVisible();
  await expect(page.getByLabel("文档", { exact: true })).toBeChecked();

  const input = page.getByLabel("问题或关键词");
  await input.fill("A 项目 延期");
  await input.press("Control+Enter");
  await expect(page.getByText("A 项目上线计划与风险")).toBeVisible();
  const loadMore = page.getByRole("button", { name: /继续加载 1 个来源的下一页/ });
  await expect(loadMore).toBeVisible();
  await loadMore.click();
  await expect(page.getByText("A 项目：执行回归测试")).toBeVisible();
  await page.getByRole("button", { name: "读取预览" }).first().click();
  const preview = page.locator(".preview-panel");
  await expect(preview).toBeVisible();
  await expect(preview.getByText(/测试环境/)).toBeVisible();

  await page.getByRole("button", { name: "运行 Doctor" }).click();
  await expect(page.getByRole("button", { name: "运行 Doctor" })).toBeEnabled();
  await expect(page.getByRole("heading", { name: "Session 历史" })).toBeVisible();
  await page.getByText("查看 8 个 Provider", { exact: true }).click();
  const sidebarMetrics = await page.locator(".sidebar").evaluate((element) => ({
    clientHeight: element.clientHeight,
    overflowY: getComputedStyle(element).overflowY,
    scrollHeight: element.scrollHeight,
  }));
  expect(sidebarMetrics.overflowY).toBe("auto");
  expect(sidebarMetrics.scrollHeight).toBeGreaterThan(sidebarMetrics.clientHeight);
  await input.fill("临时未提交内容");
  await page.getByRole("button", { name: /A 项目 延期/ }).first().click();
  await expect(input).toHaveValue("A 项目 延期");
  await expect(page.getByText("A 项目上线计划与风险")).toBeVisible();
  await page.getByText("查看 8 个 Provider", { exact: true }).click();

  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (window as typeof window & { axe: { run: () => Promise<{ violations: Array<{ id: string; help: string; nodes: Array<{ target: string[]; failureSummary: string }> }> }> } }).axe.run());
  const violationSummary = violations.violations.map((item) => ({ id: item.id, help: item.help, nodes: item.nodes.map((node) => ({ target: node.target, failure: node.failureSummary })) }));
  expect(violationSummary, JSON.stringify(violationSummary, null, 2)).toEqual([]);
});

test("深度检索展示可追溯 Evidence", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/");
  await page.getByRole("radio", { name: /深度检索/ }).click();
  await page.getByLabel("问题或关键词").fill("A 项目为什么延期");
  await page.getByRole("button", { name: "开始深度检索" }).click();
  await expect(page.getByRole("heading", { name: "证据整理" })).toBeVisible();
  await expect(page.locator(".evidence-id").first()).toContainText("ev_");
  await expect(page.getByText(/基于已读取内容/).first()).toBeVisible();
});

test("离线 Demo 完成 Ask、Claim 与 Citation 全流程", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/");
  await page.getByRole("radio", { name: /AI 问答/ }).click();
  await page.getByLabel("问题或关键词").fill("A 项目为什么延期");
  await page.getByRole("button", { name: "开始AI 问答" }).click();

  await expect(page.getByRole("heading", { name: "回答与引用" })).toBeVisible();
  await expect(page.getByText(/这是离线演示回答/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "可核验结论" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "引用", exact: true })).toBeVisible();
  await expect(page.locator(".citation-id").first()).toContainText("ev_");
  await expect(page.getByText(/离线 Demo Answerer/)).toBeVisible();
});

test("390x844 键盘流程无横向溢出", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const input = page.getByLabel("问题或关键词");
  await input.fill("A 项目 延期");
  await input.press("Control+Enter");
  await expect(page.getByText("A 项目上线计划与风险")).toBeVisible();
  const mobileSidebarStyles = await page.locator(".sidebar").evaluate((element) => ({
    maxHeight: getComputedStyle(element).maxHeight,
    overflowY: getComputedStyle(element).overflowY,
    position: getComputedStyle(element).position,
  }));
  expect(mobileSidebarStyles).toEqual({
    maxHeight: "none",
    overflowY: "visible",
    position: "static",
  });
  await input.fill("临时未提交内容");
  await page.getByRole("button", { name: /A 项目 延期/ }).first().click();
  await expect(input).toHaveValue("A 项目 延期");
  const layout = await page.evaluate(() => {
    const width = document.documentElement.clientWidth;
    const overflow = Array.from(document.querySelectorAll<HTMLElement>("body *")).filter((element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && (rect.left < -1 || rect.right > width + 1);
    });
    return { innerWidth: window.innerWidth, clientWidth: width, scrollWidth: document.documentElement.scrollWidth, overflow: overflow.map((element) => element.tagName + "." + element.className) };
  });
  expect(layout.innerWidth).toBe(390);
  expect(layout.clientWidth).toBe(390);
  expect(layout.scrollWidth).toBe(390);
  expect(layout.overflow).toEqual([]);
});

test("深色模式保持语义层级与无障碍", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/");

  const colors = await page.evaluate(() => ({
    body: getComputedStyle(document.body).backgroundColor,
    workspace: getComputedStyle(document.querySelector(".primary-column")!).backgroundColor,
    text: getComputedStyle(document.querySelector("h1")!).color,
  }));
  expect(colors).toEqual({
    body: "rgb(25, 25, 25)",
    workspace: "rgb(17, 17, 17)",
    text: "rgb(242, 242, 242)",
  });

  await page.addScriptTag({ content: axe.source });
  const violations = await page.evaluate(async () => (window as typeof window & { axe: { run: () => Promise<{ violations: Array<{ id: string; help: string; nodes: Array<{ target: string[]; failureSummary: string }> }> }> } }).axe.run());
  expect(violations.violations, JSON.stringify(violations.violations, null, 2)).toEqual([]);
});
