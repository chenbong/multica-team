export const ONETOOL_BROWSE_URL = "https://console.cloud.baidu-int.com/onetool/skills";

export function normalizeOneToolInput(input: string): string {
  const text = input.trim();
  return text.match(/^下载并安装此\s*skill\s*[:：]?\s+(https:\/\/\S+)$/i)?.[1] ?? text;
}

export function isOneToolInput(input: string): boolean {
  try {
    return ["mcp-proxy.baidu-int.com", "console.cloud.baidu-int.com"].includes(new URL(normalizeOneToolInput(input)).hostname);
  } catch {
    return false;
  }
}

export function isOneToolDownloadUrl(input: string): boolean {
  const raw = normalizeOneToolInput(input);
  if (/[\\%]/.test(raw) || raw.includes("/../") || raw.includes("/./")) return false;
  try {
    const url = new URL(raw);
    if (url.protocol !== "https:" || url.hostname !== "mcp-proxy.baidu-int.com" ||
      url.username || url.password || url.search || raw.includes("?") || url.hash ||
      (url.port && url.port !== "443")) return false;
    const parts = url.pathname.split("/").slice(1);
    const part = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
    if (parts.length !== 7 || parts.slice(0, 4).join("/") !== "api/v1/skills/download" ||
      !part.test(parts[6]!) || !/\.zip$/i.test(parts[6]!)) return false;
    if (parts[4] === "permanent") {
      return /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(parts[5]!);
    }
    return /^[1-9]\d{0,18}$/.test(parts[4]!) && BigInt(parts[4]!) <= BigInt("9223372036854775807") && part.test(parts[5]!);
  } catch {
    return false;
  }
}
