/** Validate navigation without dropping query parameters or hash-router state. */
export function webPanelUrl(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  const value = raw.trim();
  if (!value || value.length > 4096 || new TextEncoder().encode(value).byteLength > 4096) return null;
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (code <= 0x20 || code === 0x7f) return null;
  }
  try {
    const parsed = new URL(value);
    if (!["https:", "http:"].includes(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password) return null;
    // Preserve the supplied bytes. Canonical identity and display navigation
    // must not accidentally discard a page's own routing/auth parameters.
    return value;
  } catch {
    return null;
  }
}

/** Same-host pages stay as links: cookies are host-scoped, not port-scoped. */
export function canEmbedWebPanel(raw: unknown, parentOrigin: string): boolean {
  const value = webPanelUrl(raw);
  if (!value) return false;
  const child = new URL(value);
  const parent = new URL(parentOrigin);
  return child.hostname !== parent.hostname && !(parent.protocol === "https:" && child.protocol !== "https:");
}
