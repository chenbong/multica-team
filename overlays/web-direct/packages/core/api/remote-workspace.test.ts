import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

describe("remote workspace API", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("sends structured references and accepts the authoritative persisted content", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ message_id: "m", task_id: "t", created_at: "2026-10-21T00:00:00Z", content: "question\nverified snapshot" }), { status: 201 }));
    vi.stubGlobal("fetch", fetch);
    const context = { binding: "binding", references: [{ path: "src/main.ts", kind: "file" as const, revision: "hash", start_line: 2, end_line: 4 }] };
    const result = await new ApiClient("https://api.example.test").sendChatMessage("s", "question", undefined, context);
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({ content: "question", workspace_context: context });
    expect(result.content).toBe("question\nverified snapshot");
  });
  it("requests only a session-relative path and forwards cancellation", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ binding: "b", root_name: "project", entries: [] })));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    await new ApiClient("https://api.example.test").chatWorkspace("session", { operation: "list", path: "src", binding: "b", offset: 200 }, controller.signal);
    expect(String(fetch.mock.calls[0]![0])).toContain("/api/chat/sessions/session/workspace");
    const init = fetch.mock.calls[0]![1];
    expect(init.signal).toBe(controller.signal);
    expect(JSON.parse(init.body)).toEqual({ operation: "list", path: "src", binding: "b", offset: 200 });
  });
});
