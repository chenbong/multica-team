// @vitest-environment jsdom
import { Blob as NodeBlob } from "node:buffer";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SkillDownload } from "./skill-download";

const mocks = vi.hoisted(() => ({ fetchQuery: vi.fn(), error: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ fetchQuery: mocks.fetchQuery }) }));
vi.mock("@multica/core/workspace/queries", () => ({ skillDetailOptions: (ws: string, id: string) => ({ queryKey: [ws, id] }) }));
vi.mock("sonner", () => ({ toast: { error: mocks.error } }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: (select: (v: unknown) => string) => select({ actions: { download: "下载", download_failed: "无法下载 skill" } }) }) }));

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("downloads saved UTF-8 content and nested files without filtering files", async () => {
  mocks.fetchQuery.mockResolvedValue({ name: "测试", content: "---\nname: test\n---\n正文\n", files: [{ path: "scripts/run.sh", content: "echo hello\n" }, { path: "assets/data.svg", content: "<svg/>" }] });
  let result: NodeBlob | undefined;
  vi.stubGlobal("Blob", NodeBlob);
  vi.stubGlobal("URL", { createObjectURL: (blob: NodeBlob) => { result = blob; return "blob:test"; }, revokeObjectURL: vi.fn() });
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  render(<SkillDownload skillId="skill" wsId="workspace" />);
  fireEvent.click(screen.getByRole("button", { name: "下载" }));
  await waitFor(() => expect(click).toHaveBeenCalledOnce());
  expect(mocks.fetchQuery).toHaveBeenCalledWith({ queryKey: ["workspace", "skill"], staleTime: 0 });
  const bytes = new Uint8Array(await result!.arrayBuffer());
  const view = new DataView(bytes.buffer);
  const decoded: Record<string, string> = {};
  let offset = 0;
  while (view.getUint32(offset, true) === 0x04034b50) {
    const size = view.getUint32(offset + 18, true);
    const nameLength = view.getUint16(offset + 26, true);
    const start = offset + 30 + nameLength;
    const decoder = new TextDecoder();
    decoded[decoder.decode(bytes.slice(offset + 30, start))] = decoder.decode(bytes.slice(start, start + size));
    offset = start + size;
  }
  expect(decoded).toEqual({ "SKILL.md": "---\nname: test\n---\n正文\n", "scripts/run.sh": "echo hello\n", "assets/data.svg": "<svg/>" });
});
