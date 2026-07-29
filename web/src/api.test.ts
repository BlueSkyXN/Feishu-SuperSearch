import { afterEach, describe, expect, it, vi } from "vitest";
import { api, readEventStream } from "./api";
import { researchResult } from "./test/fixtures";

function streamResponse(text: string, cuts: number[]): Response {
  const bytes = new TextEncoder().encode(text);
  const chunks: Uint8Array[] = [];
  let start = 0;
  for (const cut of cuts) {
    chunks.push(bytes.slice(start, Math.min(cut, bytes.length)));
    start = Math.min(cut, bytes.length);
  }
  if (start < bytes.length) chunks.push(bytes.slice(start));
  let index = 0;
  return {
    ok: true,
    status: 200,
    headers: new Headers({ "Content-Type": "text/event-stream" }),
    body: {
      getReader: () => ({
        read: async () => index < chunks.length
          ? { value: chunks[index++], done: false }
          : { value: undefined, done: true },
      }),
    },
  } as unknown as Response;
}

describe("SSE client", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("parses CRLF, multiline data, Chinese text, and arbitrary chunks", async () => {
    const stream = [
      "event: progress\r\n",
      "data: {\"type\":\"progress\",\r\n",
      "data: \"phase\":\"planning\",\"message\":\"正在规划\",\"time\":\"2026-07-29T00:00:00Z\"}\r\n\r\n",
      "event: ignored\ndata: {\"ok\":true}\n\n",
      `event: result\ndata: ${JSON.stringify(researchResult)}\n\n`,
    ].join("");
    const events: string[] = [];
    const result = await readEventStream<typeof researchResult>(streamResponse(stream, [1, 7, 31, 67, 83, 121]), (event) => {
      events.push(`${event.phase}:${event.message}`);
    });
    expect(events).toEqual(["planning:正在规划"]);
    expect(result.summary).toBe(researchResult.summary);
  });

  it("maps an error terminal event to APIError", async () => {
    const response = streamResponse('event: error\ndata: {"ok":false,"error":{"type":"deadline_exceeded","message":"已超时"}}\n\n', [11, 29]);
    await expect(readEventStream(response)).rejects.toMatchObject({ name: "APIError", type: "deadline_exceeded", message: "已超时" });
  });

  it("rejects EOF before a result terminal event", async () => {
    const response = streamResponse('event: progress\ndata: {"type":"progress","phase":"planning","time":"2026-07-29T00:00:00Z"}\n\n', [8]);
    await expect(readEventStream(response)).rejects.toMatchObject({ name: "APIError", type: "stream_incomplete" });
  });

  it("keeps JSON success responses as a compatibility fallback", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true,
      status: 200,
      headers: new Headers({ "Content-Type": "application/json" }),
      json: async () => researchResult,
    } as Response));
    vi.stubGlobal("fetch", fetchMock);
    const result = await api.research("A 项目为什么延期", ["docs"]);
    expect(result.summary).toBe(researchResult.summary);
    expect(fetchMock.mock.calls[0][1]?.headers).toMatchObject({ Accept: "text/event-stream" });
  });

  it("preserves AbortError from the stream reader", async () => {
    const abort = new DOMException("aborted", "AbortError");
    const response = {
      ok: true,
      status: 200,
      body: { getReader: () => ({ read: async () => { throw abort; } }) },
    } as unknown as Response;
    await expect(readEventStream(response)).rejects.toBe(abort);
  });
});
