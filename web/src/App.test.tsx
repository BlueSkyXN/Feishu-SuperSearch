import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { askResult, candidate, capabilities, evidence, health, researchResult, searchResult } from "./test/fixtures";

function response(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}

function installFetchRouter(overrides: Record<string, unknown> = {}) {
  const routes: Record<string, unknown> = {
    "GET /v1/health": health,
    "GET /v1/capabilities": capabilities,
    "GET /v1/sessions": { sessions: [] },
    ...overrides,
  };
  const mock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const path = url.replace(/^https?:\/\/[^/]+/, "");
    const method = init?.method || "GET";
    const key = `${method} ${path}`;
	if (!(key in routes)) return response({ error: { message: `No mock for ${key}` } }, 500);
	const route = routes[key];
	if (typeof route === "function") return (route as (init?: RequestInit) => Response | Promise<Response>)(init);
	return response(route);
  });
  vi.stubGlobal("fetch", mock);
  return mock;
}

async function waitForBootstrap() {
  await screen.findAllByText("mock · memory");
  await waitFor(() => expect(screen.getByLabelText("文档")).toBeChecked());
}

describe("SuperFeishuSearch web app", () => {
  beforeEach(() => {
    installFetchRouter();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders the local safety boundary and passes a basic axe scan", async () => {
    const { container } = render(<App />);
    await waitForBootstrap();
    expect(screen.getByText("仅限本机回环访问")).toBeVisible();
    expect(screen.getByRole("heading", { name: "运行时与 Doctor" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "Session 历史" })).toBeVisible();

    const scan = await axe.run(container);
    expect(scan.violations.map((violation) => violation.id)).toEqual([]);
  }, 10_000);

  it("runs Search, reports partial sources, and previews an artifact", async () => {
    installFetchRouter({
      "POST /v1/search": searchResult,
      "POST /v1/fetch": {
        session_id: searchResult.session_id,
        partial: false,
        items: [
          {
            ref: candidate.ref,
            artifact: {
              ref: candidate.ref,
              projection: ["content"],
              chunks: [{ id: "chunk-1", kind: "text", text: "测试环境交付延迟三天。" }],
            },
          },
        ],
      },
    });
    const user = userEvent.setup();
    render(<App />);
    await waitForBootstrap();

    await user.type(screen.getByLabelText("问题或关键词"), "A 项目延期");
    await user.click(screen.getByRole("button", { name: "开始搜索" }));

    expect(await screen.findByText("A 项目复盘")).toBeVisible();
    expect(screen.getByText("结果为部分成功")).toBeVisible();
    expect(screen.getByText("缺少消息读取权限")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "读取预览" }));
    expect(await screen.findByText("测试环境交付延迟三天。")).toBeVisible();
  });

  it("renders Research evidence with a source citation", async () => {
    installFetchRouter({ "POST /v1/research": researchResult });
    const user = userEvent.setup();
    render(<App />);
    await waitForBootstrap();

    await user.click(screen.getByRole("radio", { name: /深度检索/ }));
    await user.type(screen.getByLabelText("问题或关键词"), "A 项目为什么延期");
    await user.click(screen.getByRole("button", { name: "开始深度检索" }));

    expect(await screen.findByText(researchResult.summary)).toBeVisible();
    expect(screen.getByText("ev_a-project-delay")).toBeVisible();
    expect(screen.getByRole("link", { name: /文档/ })).toHaveAttribute("href", "https://example.invalid/doc-1");
  });

  it("shows a server progress event before the Research result", async () => {
	let releaseResult!: () => void;
	const gate = new Promise<void>((resolve) => { releaseResult = resolve; });
	installFetchRouter({
	  "POST /v1/research": () => {
		const chunks = [
		  new TextEncoder().encode('event: progress\ndata: {"type":"progress","phase":"planning","message":"服务端正在规划","time":"2026-07-29T00:00:00Z"}\n\n'),
		  new TextEncoder().encode(`event: result\ndata: ${JSON.stringify(researchResult)}\n\n`),
		];
		let index = 0;
		return {
		  ok: true,
		  status: 200,
		  headers: new Headers({ "Content-Type": "text/event-stream" }),
		  body: {
			getReader: () => ({
			  read: async () => {
				if (index === 1) await gate;
				return index < chunks.length
				  ? { value: chunks[index++], done: false }
				  : { value: undefined, done: true };
			  },
			}),
		  },
		} as unknown as Response;
	  },
	});
	const user = userEvent.setup();
	render(<App />);
	await waitForBootstrap();
	await user.click(screen.getByRole("radio", { name: /深度检索/ }));
	await user.type(screen.getByLabelText("问题或关键词"), "A 项目为什么延期");
	await user.click(screen.getByRole("button", { name: "开始深度检索" }));
	expect((await screen.findAllByText("服务端正在规划")).length).toBeGreaterThan(0);
	releaseResult();
	expect(await screen.findByText(researchResult.summary)).toBeVisible();
  });

  it("renders Ask claims and citations when AI Answer is enabled", async () => {
    installFetchRouter({ "POST /v1/ask": askResult });
    const user = userEvent.setup();
    render(<App />);
    await waitForBootstrap();

    await user.click(screen.getByRole("radio", { name: /AI 问答/ }));
    await user.type(screen.getByLabelText("问题或关键词"), "A 项目为什么延期");
    await user.click(screen.getByRole("button", { name: "开始AI 问答" }));

    expect(await screen.findByText(askResult.answer.text)).toBeVisible();
    expect(screen.getByText("测试环境晚交付三天")).toBeVisible();
    expect(screen.getAllByText(evidence.id).length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: "查看来源" })).toBeVisible();
  });
});
