import { describe, expect, it } from "vitest";
import {
  DEFAULT_MEMORY_LIMITS,
  draftFromServer,
  draftSignature,
  draftToRequest,
  mergeMemoryFiles,
  normalizeMemoryPath,
  readMemoryUploads,
  runtimeMemoryToDraft,
  sanitizeMemoryFileName,
  uniqueMemoryPath,
  validateMemoryDraft,
  type DraftMemoryFile,
} from "./agent-memory";

const file = (path: string, content = "", extra: Partial<DraftMemoryFile> = {}): DraftMemoryFile => ({
  localId: `id-${path}`,
  path,
  content,
  source: "manual",
  enabled: true,
  ...extra,
});

describe("normalizeMemoryPath", () => {
  it("accepts and canonicalizes relative paths", () => {
    expect(normalizeMemoryPath(" notes\\today.md ")).toEqual({ ok: true, path: "notes/today.md" });
    expect(normalizeMemoryPath("a/./b/../c.md")).toEqual({ ok: true, path: "a/c.md" });
    expect(normalizeMemoryPath("项目/记忆.md")).toEqual({ ok: true, path: "项目/记忆.md" });
  });

  it("rejects the same paths the server rejects", () => {
    expect(normalizeMemoryPath("")).toEqual({ ok: false, error: "empty" });
    expect(normalizeMemoryPath("/etc/passwd")).toEqual({ ok: false, error: "absolute" });
    expect(normalizeMemoryPath("../x.md")).toEqual({ ok: false, error: "outside" });
    expect(normalizeMemoryPath("C:/x.md")).toEqual({ ok: false, error: "invalid_char" });
    expect(normalizeMemoryPath(".git/config")).toEqual({ ok: false, error: "bad_segment" });
    expect(normalizeMemoryPath("a/".repeat(9) + "x.md")).toEqual({ ok: false, error: "too_deep" });
  });
});

describe("validateMemoryDraft", () => {
  it("reports duplicates, folder clashes and size limits", () => {
    const issues = validateMemoryDraft(
      [
        file("A.md"),
        file("a.md"),
        file("notes"),
        file("notes/x.md"),
        file("big.md", "x".repeat(DEFAULT_MEMORY_LIMITS.max_file_bytes + 1)),
      ],
      DEFAULT_MEMORY_LIMITS,
    );
    const kinds = issues.map((i) => i.kind).sort();
    expect(kinds).toEqual(["duplicate", "file_too_large", "folder_conflict"]);
  });

  it("is clean for a valid set", () => {
    expect(validateMemoryDraft([file("CLAUDE.md", "rules"), file("notes/a.md")])).toEqual([]);
  });
});

describe("uniqueMemoryPath / mergeMemoryFiles", () => {
  it("suffixes before the extension", () => {
    expect(uniqueMemoryPath("notes.md", new Set(["notes.md", "notes-2.md"]))).toBe("notes-3.md");
    expect(uniqueMemoryPath("README", new Set(["readme"]))).toBe("README-2");
  });

  it("replaces a re-import of the same source and renames other collisions", () => {
    const draft = [
      file("claude/CLAUDE.md", "old", { source: "runtime", sourceRuntimeId: "rt", sourcePath: "~/.claude/CLAUDE.md" }),
      file("notes.md", "mine"),
    ];
    const { files, touched } = mergeMemoryFiles(draft, [
      ...runtimeMemoryToDraft("rt", [
        { key: "k", source_path: "~/.claude/CLAUDE.md", suggested_path: "claude/CLAUDE.md", content: "new", size_bytes: 3 },
      ]),
      { path: "notes.md", content: "uploaded", source: "upload", sourcePath: "notes.md", enabled: true },
    ]);
    expect(files).toHaveLength(3);
    expect(files[0]).toMatchObject({ path: "claude/CLAUDE.md", content: "new", localId: draft[0]!.localId });
    expect(files[2]).toMatchObject({ path: "notes-2.md", content: "uploaded", source: "upload" });
    expect(touched).toHaveLength(2);
  });
});

describe("draftSignature", () => {
  it("ignores order and local ids", () => {
    const a = [file("a.md", "1"), file("b.md", "2")];
    const b = [{ ...file("b.md", "2"), localId: "x" }, { ...file("a.md", "1"), localId: "y" }];
    expect(draftSignature(a)).toBe(draftSignature(b));
    expect(draftSignature(a)).not.toBe(draftSignature([file("a.md", "changed"), file("b.md", "2")]));
  });
});

describe("readMemoryUploads", () => {
  it("accepts text files and rejects binary or oversize ones", async () => {
    const { accepted, rejected } = await readMemoryUploads(
      [
        new File(["# Notes"], "team notes.md", { type: "text/markdown" }),
        new File([new Uint8Array([0, 1, 2])], "blob.md"),
        new File(["x".repeat(20)], "big.txt"),
      ],
      { ...DEFAULT_MEMORY_LIMITS, max_file_bytes: 10 },
    );
    expect(accepted).toEqual([
      { path: "team notes.md", content: "# Notes", source: "upload", sourcePath: "team notes.md", enabled: true },
    ]);
    expect(rejected).toEqual([
      { name: "blob.md", reason: "binary" },
      { name: "big.txt", reason: "too_large" },
    ]);
  });

  it("keeps the original name as the source path", async () => {
    const { accepted } = await readMemoryUploads([new File(["hello"], ".hidden notes.md")]);
    expect(accepted).toEqual([
      { path: "hidden notes.md", content: "hello", source: "upload", sourcePath: ".hidden notes.md", enabled: true },
    ]);
  });
});

describe("sanitizeMemoryFileName", () => {
  it("drops directories and unsafe characters", () => {
    expect(sanitizeMemoryFileName("dir/sub/a:b?.md")).toBe("a-b-.md");
    expect(sanitizeMemoryFileName("...")).toBe("memory.md");
  });
});

describe("enabled flag", () => {
  it("round-trips through the request and counts toward dirty tracking", () => {
    const on = [file("a.md", "1"), file("b.md", "2")];
    const off = [file("a.md", "1"), file("b.md", "2", { enabled: false })];
    expect(draftToRequest(off).map((f) => f.enabled)).toEqual([true, false]);
    expect(draftSignature(on)).not.toBe(draftSignature(off));
  });

  it("treats files from an older server as enabled", () => {
    const [f] = draftFromServer([
      {
        id: "1",
        path: "a.md",
        content: "",
        source: "manual",
        source_runtime_id: null,
        source_path: "",
        size_bytes: 0,
        updated_by: null,
        created_at: "",
        updated_at: "",
      } as unknown as Parameters<typeof draftFromServer>[0][number],
    ]);
    expect(f!.enabled).toBe(true);
  });

  it("keeps a disabled file disabled when it is re-imported", () => {
    const draft = [
      file("claude/CLAUDE.md", "old", {
        source: "runtime",
        sourceRuntimeId: "rt",
        sourcePath: "~/.claude/CLAUDE.md",
        enabled: false,
      }),
    ];
    const { files } = mergeMemoryFiles(
      draft,
      runtimeMemoryToDraft("rt", [
        { key: "k", source_path: "~/.claude/CLAUDE.md", suggested_path: "claude/CLAUDE.md", content: "new", size_bytes: 3 },
      ]),
    );
    expect(files[0]).toMatchObject({ content: "new", enabled: false });
  });
});
