import { webPanelUrl } from "./url";

/** Narrow compatibility rules verified in a real browser, not a remote probe. */
export function hasKnownWebPanelRestriction(raw: unknown, parentOrigin: string): boolean {
  const value = webPanelUrl(raw);
  if (!value) return false;
  try {
    const page = new URL(value);
    const parent = new URL(parentOrigin);
    const host = page.hostname.toLowerCase().replace(/\.$/, "");
    // The Baidu homepage's frame-ancestors policy excludes Team Mesh origins.
    if (host === "www.baidu.com" && page.pathname === "/") return true;
    // Knowledge pages loop through SSO when embedded cross-site. Do not apply
    // this observation to unrelated hosts, other services or same-site hosting.
    const parentHost = parent.hostname.toLowerCase().replace(/\.$/, "");
    const sameSite = parent.protocol === page.protocol && (parentHost === "baidu-int.com" || parentHost.endsWith(".baidu-int.com"));
    return host === "ku.baidu-int.com" && /^\/knowledge(?:\/|$)/.test(page.pathname) && !sameSite;
  } catch {
    return false;
  }
}
