import { beforeEach, describe, expect, it } from "vitest";
import { appendReference, referenceKey, selectionLines, useRemoteWorkspaceDrafts } from "./remote-workspace-state";

describe("remote workspace references", () => {
  beforeEach(() => useRemoteWorkspaceDrafts.setState({ drafts: {} }));
  it("deduplicates paths and selections without merging distinct line ranges", () => {
    let draft = appendReference(undefined, "a", { path: "main.ts", kind: "file" });
    draft = appendReference(draft, "a", { path: "main.ts", kind: "file", revision: "new" });
    expect(draft.references).toHaveLength(1);
    draft = appendReference(draft, "a", { path: "main.ts", kind: "file", start_line: 1, end_line: 3 });
    expect(draft.references).toHaveLength(2);
    expect(referenceKey(draft.references[0]!)).not.toBe(referenceKey(draft.references[1]!));
  });
  it("caps references and isolates account/workspace/session keys", () => {
    let draft = appendReference(undefined, "a", { path: "0", kind: "file" });
    for (let i = 1; i < 12; i++) draft = appendReference(draft, "a", { path: String(i), kind: "file" });
    expect(() => appendReference(draft, "a", { path: "13", kind: "file" })).toThrow();
    const store = useRemoteWorkspaceDrafts.getState();
    store.set("account-a/ws/session", draft);
    expect(useRemoteWorkspaceDrafts.getState().drafts["account-b/ws/session"]).toBeUndefined();
    expect(useRemoteWorkspaceDrafts.getState().drafts["account-a/ws/other"]).toBeUndefined();
  });
  it("clears only the exact successfully sent draft", () => {
    const sent = appendReference(undefined, "a", { path: "main.ts", kind: "file" });
    const newer = appendReference(sent, "a", { path: "src", kind: "directory" });
    const store = useRemoteWorkspaceDrafts.getState();
    store.set("s", newer); store.clearSent("s", sent);
    expect(useRemoteWorkspaceDrafts.getState().drafts.s).toBe(newer);
    store.clearSent("s", newer);
    expect(useRemoteWorkspaceDrafts.getState().drafts.s).toBeUndefined();
  });
  it("computes line ranges for a real DOM selection and ignores external selection", () => {
    const code = document.createElement("div"); code.textContent = "one\ntwo\nthree\n"; document.body.append(code);
    const range = document.createRange(); range.setStart(code.firstChild!, 4); range.setEnd(code.firstChild!, 8);
    const selection = window.getSelection()!; selection.removeAllRanges(); selection.addRange(range);
    expect(selectionLines(code, selection)).toEqual({ start_line: 2, end_line: 2 });
    expect(selectionLines(document.createElement("div"), selection)).toBeNull();
    selection.removeAllRanges(); code.remove();
  });
});
