// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { SkillSummary } from "@multica/core/types";
import { isOneToolDownloadUrl, isOneToolInput, normalizeOneToolInput, ONETOOL_BROWSE_URL } from "./onetool";
import { isRefreshableOrigin, originSourceUrl, readOrigin } from "./origin";

const version = "https://mcp-proxy.baidu-int.com/api/v1/skills/download/42424242/9.8.7/popo.zip";
const permanent = "https://mcp-proxy.baidu-int.com/api/v1/skills/download/permanent/11111111-2222-4333-8444-555555555555/popo.zip";

describe("OneTool package links", () => {
  it.each([version, permanent])("accepts the link and the exact copied install text: %s", url => {
    expect(isOneToolDownloadUrl(url)).toBe(true);
    expect(normalizeOneToolInput(` 下载并安装此Skill ${url}\n`)).toBe(url);
    expect(isOneToolInput(`下载并安装此Skill ${url}`)).toBe(true);
  });
  it.each([
    "https://console.cloud.baidu-int.com/onetool/skills/154",
    version.replace("https:", "http:"), version + "?token=secret", version + "#fragment", version + "?",
    version.replace("popo.zip", "../popo.zip"), version.replace("popo.zip", "%2e%2e/popo.zip"),
    version.replace("popo.zip", "popo.html"), version.replace("/42424242/", "/9223372036854775808/"),
    version.replace("mcp-proxy.baidu-int.com", "user:secret@mcp-proxy.baidu-int.com"),
    version.replace("mcp-proxy.baidu-int.com", "mcp-proxy.baidu-int.com.evil.test"),
    version.replace("mcp-proxy.baidu-int.com", "mcp-proxy.baidu-int.com:8443"),
    "下载并安装此Skill " + version + " 然后执行", "https://127.0.0.1/private.zip",
  ])("rejects non-package, unsafe, or credential-bearing links", value => {
    expect(isOneToolDownloadUrl(value)).toBe(false);
  });
  it("preserves origin labels, safe links and explicit refresh eligibility", () => {
    const skill = { config: { origin: { type: "onetool", source_url: permanent } } } as unknown as SkillSummary;
    const origin = readOrigin(skill);
    expect(origin.type).toBe("onetool");
    expect(originSourceUrl(origin)).toBe(permanent);
    expect(isRefreshableOrigin(origin)).toBe(true);
    expect(originSourceUrl({ type: "onetool", source_url: "https://evil.test/package.zip" })).toBeNull();
    expect(ONETOOL_BROWSE_URL).toBe("https://console.cloud.baidu-int.com/onetool/skills");
  });
});
