// @vitest-environment node
import { describe, expect, it } from "vitest";
import { hasKnownWebPanelRestriction } from "./embed-policy";

describe("verified web panel embedding restrictions", () => {
  const parent = "http://localhost:18084";
  it("blocks the verified Baidu homepage and cross-site knowledge pages", () => {
    for (const url of ["https://www.baidu.com/", "https://www.baidu.com/?from=panel#top", "https://www.baidu.com./", "https://ku.baidu-int.com/knowledge/a/b/c/d"]) {
      expect(hasKnownWebPanelRestriction(url, parent)).toBe(true);
    }
  });
  it("does not infer a restriction from unrelated names or unverified pages", () => {
    for (const url of ["https://www.baidu.com.example.test/", "https://example.test/?next=https://www.baidu.com/", "https://ku.baidu-int.com.example.test/knowledge/a", "https://ku.baidu-int.com/knowledge-example", "https://www.baidu.com/other", "https://pages.example.test/result"]) {
      expect(hasKnownWebPanelRestriction(url, parent)).toBe(false);
    }
  });
  it("does not apply cross-site knowledge login observations to same-site hosting", () => {
    expect(hasKnownWebPanelRestriction("https://ku.baidu-int.com/knowledge/a", "https://team.baidu-int.com")).toBe(false);
    expect(hasKnownWebPanelRestriction("https://ku.baidu-int.com/knowledge/a", "http://team.baidu-int.com")).toBe(true);
    expect(hasKnownWebPanelRestriction("https://ku.baidu-int.com/knowledge/a", "https://not-baidu-int.com")).toBe(true);
  });
  it("fails safely for malformed input without performing a network request", () => {
    for (const url of [null, "not a URL", "javascript:alert(1)", "https://user:pass@www.baidu.com/"]) expect(hasKnownWebPanelRestriction(url, parent)).toBe(false);
    expect(hasKnownWebPanelRestriction("https://www.baidu.com/", "invalid")).toBe(false);
  });
});
