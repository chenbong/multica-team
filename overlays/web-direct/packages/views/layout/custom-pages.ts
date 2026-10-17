import { useCallback, useMemo, useSyncExternalStore } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { issueKeys } from "@multica/core/issues/queries";
import type { Issue } from "@multica/core/types";

/**
 * Custom pages: any user-built page (popo vibe-coding output, a live chart…)
 * that people bind to issues and pin to their own dashboard.
 *
 * - The binding lives on the issue, in upstream's existing issue metadata:
 *   key `page.<id>`, value = the page's canonical URL. Many-to-many falls out
 *   naturally, and the reverse lookup is the upstream issue-list metadata
 *   filter. Agents bind with the plain CLI:
 *     multica issue metadata set <issue> page.popo.<slug> https://<slug>.popo.baidu-int.com/
 * - POPOs (the viewer's own page list) and the dashboard are two separate
 *   lists, both in this browser only (localStorage), per workspace. A POPOs
 *   page reaches the dashboard only when the viewer pins it.
 * - The page itself is framed cross-origin and never talks to Multica.
 */
export interface PageRef {
  id: string;
  key: string;
  url: string;
  title: string;
}

// Same policy as the custom-app frame: the page keeps its own origin and may
// open tabs, but cannot navigate the Multica tab. Only cross-origin pages are
// ever framed (`frameableUrl`), so allow-same-origin is the page's own origin.
export const PAGE_FRAME_SANDBOX =
  "allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals";

export const PAGE_KEY_PREFIX = "page.";
export const MAX_DASHBOARD_CARDS = 12;
const POPO_HOST = /^([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.popo\.baidu-int\.com$/;
// Metadata keys are at most 64 chars: "page.popo." leaves 54 for the slug.
const MAX_POPO_SLUG_IN_KEY = 54;

const HAS_SCHEME = /^[a-z][a-z0-9+.-]*:\/\//i;

/**
 * Normalised http(s) URL, or null for anything that must never be framed.
 * A pasted link without a scheme (`ku.baidu-int.com/...`) is read as https.
 */
export function canonicalPageUrl(raw: unknown): string | null {
  if (typeof raw !== "string" || !raw.trim()) return null;
  const text = raw.trim();
  try {
    const url = new URL(HAS_SCHEME.test(text) ? text : `https://${text.replace(/^\/\//, "")}`);
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    if (url.username || url.password) return null;
    url.hash = "";
    return url.href;
  } catch {
    return null;
  }
}

/** Only cross-origin pages are framed, so they can never reach Multica's DOM or cookies. */
export function frameableUrl(raw: unknown): string | null {
  const href = canonicalPageUrl(raw);
  if (!href || typeof window === "undefined") return href;
  return new URL(href).origin === window.location.origin ? null : href;
}

// cyrb53: small, stable, synchronous string hash (no crypto needed for an id).
function hash(text: string): string {
  let h1 = 0xdeadbeef;
  let h2 = 0x41c6ce57;
  for (let i = 0; i < text.length; i++) {
    const ch = text.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36).padStart(11, "0");
}

function popoSlug(href: string): string | null {
  const url = new URL(href);
  const match = POPO_HOST.exec(url.hostname);
  return match && url.protocol === "https:" ? match[1]! : null;
}

/** `popo.<slug>` for a popo work's home page, otherwise `url.<hash>`. */
export function pageIdForUrl(href: string): string {
  const url = new URL(href);
  const slug = popoSlug(href);
  if (slug && url.pathname === "/" && !url.search && slug.length <= MAX_POPO_SLUG_IN_KEY) return `popo.${slug}`;
  return `url.${hash(href)}`;
}

export function pageTitle(href: string): string {
  const url = new URL(href);
  const slug = popoSlug(href);
  const path = url.pathname === "/" ? "" : url.pathname;
  return slug ? `${slug}${path}` : `${url.host}${path}`;
}

export function pageRef(raw: unknown): PageRef | null {
  const url = canonicalPageUrl(raw);
  if (!url) return null;
  const id = pageIdForUrl(url);
  return { id, key: PAGE_KEY_PREFIX + id, url, title: pageTitle(url) };
}

/** Every page bound to an issue, read from its metadata. */
export function pagesFromMetadata(metadata: Record<string, unknown> | null | undefined): PageRef[] {
  if (!metadata) return [];
  const pages: PageRef[] = [];
  for (const [key, value] of Object.entries(metadata)) {
    if (!key.startsWith(PAGE_KEY_PREFIX)) continue;
    const url = canonicalPageUrl(value);
    if (!url) continue;
    pages.push({ id: key.slice(PAGE_KEY_PREFIX.length), key, url, title: pageTitle(url) });
  }
  return pages.sort((a, b) => a.title.localeCompare(b.title));
}

export function pageViewPath(workspaceSlug: string, url: string): string {
  return `/${encodeURIComponent(workspaceSlug)}/apps/view?url=${encodeURIComponent(url)}`;
}

export function dashboardPath(workspaceSlug: string): string {
  return `/${encodeURIComponent(workspaceSlug)}/apps`;
}

const linkedIssuesKey = (key: string, url: string) => ["custom-pages", "issues", key, url] as const;

/** Issues bound to one page, via the upstream metadata filter. */
export function useLinkedIssues(url: string | null) {
  const ref = url ? pageRef(url) : null;
  return useQuery({
    queryKey: linkedIssuesKey(ref?.key ?? "", ref?.url ?? ""),
    enabled: !!ref,
    queryFn: async () => (await api.listIssues({ metadata: { [ref!.key]: ref!.url }, limit: 50 })).issues,
    staleTime: 30_000,
  });
}

type LinkedIssue = Pick<Issue, "id" | "identifier" | "title">;

const linkedIssuesQuery = (url: string) => {
  const ref = pageRef(url)!;
  return {
    queryKey: linkedIssuesKey(ref.key, ref.url),
    queryFn: async () => (await api.listIssues({ metadata: { [ref.key]: ref.url }, limit: 50 })).issues as LinkedIssue[],
    staleTime: 30_000,
  };
};

/**
 * Issues bound to each of several pages (one upstream list call per page,
 * shared with `useLinkedIssues`): which issues each card links to, and every
 * such issue once, for the dashboard's issue filter.
 */
export function useCardsLinkedIssues(urls: string[]) {
  const valid = urls.filter((url) => pageRef(url));
  const results = useQueries({ queries: valid.map(linkedIssuesQuery) });
  const stamp = results.map((r) => r.dataUpdatedAt).join(",");
  return useMemo(() => {
    const byUrl = new Map<string, string[]>();
    const issues = new Map<string, LinkedIssue>();
    valid.forEach((url, i) => {
      const list = results[i]?.data ?? [];
      byUrl.set(pageRef(url)!.url, list.map((issue) => issue.id));
      for (const issue of list) issues.set(issue.id, issue);
    });
    return {
      byUrl,
      issues: [...issues.values()].sort((a, b) => a.identifier.localeCompare(b.identifier, undefined, { numeric: true })),
      isLoading: results.some((r) => r.isLoading),
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [valid.join("\n"), stamp]);
}

export function usePageBinding(issue: Pick<Issue, "id" | "workspace_id">) {
  const queryClient = useQueryClient();
  const refresh = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: issueKeys.detail(issue.workspace_id, issue.id) }),
    queryClient.invalidateQueries({ queryKey: ["custom-pages", "issues"] }),
  ]);
  const bind = useMutation({
    mutationFn: async (raw: string) => {
      const ref = pageRef(raw);
      if (!ref) throw new Error("invalid_url");
      await api.setIssueMetadataKey(issue.id, ref.key, ref.url);
      return ref;
    },
    onSuccess: refresh,
  });
  const unbind = useMutation({
    mutationFn: (key: string) => api.deleteIssueMetadataKey(issue.id, key),
    onSuccess: refresh,
  });
  return { bind, unbind };
}

// ---- Personal dashboard (this browser only) ----

/** Card size: width in % of the dashboard row, height of the page area in px. */
export const CARD_WIDTH = { min: 20, max: 100, default: 50 } as const;
export const CARD_HEIGHT = { min: 160, max: 1600, default: 480 } as const;

/** Where a card was pinned from; the dashboard filters on it. */
export type CardSource = "popos" | "issue" | "manual";
export const CARD_SOURCES: readonly CardSource[] = ["popos", "issue", "manual"];

export interface DashboardCard {
  id: string;
  url: string;
  title: string;
  width: number;
  height: number;
  source: CardSource;
}
export type AddCardResult = "added" | "exists" | "full" | "invalid";

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export function clampCardSize(width: number, height: number): { width: number; height: number } {
  return {
    width: Math.round(clamp(width, CARD_WIDTH.min, CARD_WIDTH.max) * 10) / 10,
    height: Math.round(clamp(height, CARD_HEIGHT.min, CARD_HEIGHT.max)),
  };
}

// v1 cards stored preset sizes (width 1|2|3 thirds-ish, height s|m|l).
const LEGACY_WIDTH: Record<number, number> = { 1: 100 / 3, 2: 50, 3: 100 };
const LEGACY_HEIGHT: Record<string, number> = { s: 320, m: 480, l: 720 };

function cardSize(card: { width?: unknown; height?: unknown }) {
  const rawWidth = card.width;
  const rawHeight = card.height;
  const width = typeof rawWidth === "number" && Number.isFinite(rawWidth)
    ? (LEGACY_WIDTH[rawWidth] ?? rawWidth) : CARD_WIDTH.default;
  const height = typeof rawHeight === "number" && Number.isFinite(rawHeight)
    ? rawHeight : typeof rawHeight === "string" ? (LEGACY_HEIGHT[rawHeight] ?? CARD_HEIGHT.default) : CARD_HEIGHT.default;
  return clampCardSize(width, height);
}

const STORAGE_EVENT = "multica:custom-pages";
const storageKey = (slug: string) => `multica:custom-dashboard:v1:${slug}`;
const EMPTY: DashboardCard[] = [];
const cache = new Map<string, { raw: string | null; cards: DashboardCard[] }>();

function parseCards(raw: string | null): DashboardCard[] {
  if (!raw) return EMPTY;
  try {
    const list: unknown = JSON.parse(raw);
    if (!Array.isArray(list)) return EMPTY;
    const cards: DashboardCard[] = [];
    for (const item of list) {
      const card = item as Partial<DashboardCard>;
      const url = canonicalPageUrl(card?.url);
      if (!url || cards.some((c) => c.url === url)) continue;
      cards.push({
        id: pageIdForUrl(url),
        url,
        title: typeof card.title === "string" && card.title.trim() ? card.title.trim().slice(0, 60) : pageTitle(url),
        ...cardSize(card),
        // Cards saved before sources existed count as added by hand.
        source: CARD_SOURCES.includes(card.source as CardSource) ? card.source! : "manual",
      });
      if (cards.length >= MAX_DASHBOARD_CARDS) break;
    }
    return cards;
  } catch {
    return EMPTY;
  }
}

export function readDashboard(slug: string): DashboardCard[] {
  let raw: string | null = null;
  try {
    raw = window.localStorage.getItem(storageKey(slug));
  } catch {
    raw = null;
  }
  const hit = cache.get(slug);
  if (hit && hit.raw === raw) return hit.cards;
  const cards = parseCards(raw);
  cache.set(slug, { raw, cards });
  return cards;
}

function writeDashboard(slug: string, cards: DashboardCard[]) {
  window.localStorage.setItem(storageKey(slug), JSON.stringify(cards));
  window.dispatchEvent(new Event(STORAGE_EVENT));
}

function subscribe(callback: () => void) {
  window.addEventListener("storage", callback);
  window.addEventListener(STORAGE_EVENT, callback);
  return () => {
    window.removeEventListener("storage", callback);
    window.removeEventListener(STORAGE_EVENT, callback);
  };
}

export function addDashboardCard(slug: string, rawUrl: string, title?: string, source: CardSource = "manual"): AddCardResult {
  const url = canonicalPageUrl(rawUrl);
  if (!url) return "invalid";
  const cards = readDashboard(slug);
  if (cards.some((card) => card.url === url)) return "exists";
  if (cards.length >= MAX_DASHBOARD_CARDS) return "full";
  writeDashboard(slug, [...cards, {
    id: pageIdForUrl(url), url, title: title?.trim().slice(0, 60) || pageTitle(url),
    width: CARD_WIDTH.default, height: CARD_HEIGHT.default, source,
  }]);
  return "added";
}

export function useDashboard(slug: string | null) {
  const cards = useSyncExternalStore(subscribe, () => (slug ? readDashboard(slug) : EMPTY), () => EMPTY);
  const update = useCallback((id: string, patch: Partial<Omit<DashboardCard, "id" | "url">>) => {
    if (!slug) return;
    writeDashboard(slug, readDashboard(slug).map((card) => {
      if (card.id !== id) return card;
      const next = { ...card, ...patch };
      return { ...next, ...clampCardSize(next.width, next.height) };
    }));
  }, [slug]);
  const remove = useCallback((id: string) => {
    if (!slug) return;
    writeDashboard(slug, readDashboard(slug).filter((card) => card.id !== id));
  }, [slug]);
  const removeUrl = useCallback((url: string) => {
    if (!slug) return;
    const href = canonicalPageUrl(url);
    writeDashboard(slug, readDashboard(slug).filter((card) => card.url !== href));
  }, [slug]);
  const move = useCallback((id: string, delta: -1 | 1) => {
    if (!slug) return;
    const next = [...readDashboard(slug)];
    const from = next.findIndex((card) => card.id === id);
    const to = from + delta;
    if (from < 0 || to < 0 || to >= next.length) return;
    [next[from], next[to]] = [next[to]!, next[from]!];
    writeDashboard(slug, next);
  }, [slug]);
  const add = useCallback((url: string, title?: string, source?: CardSource) =>
    (slug ? addDashboardCard(slug, url, title, source) : "invalid" as const), [slug]);
  return { cards, add, update, remove, removeUrl, move };
}

// ---- POPOs: the viewer's own page list (this browser only) ----

export const MAX_POPOS = 50;
export interface PoposPage {
  id: string;
  url: string;
  title: string;
}

const poposKey = (slug: string) => `multica:custom-popos:v1:${slug}`;
const NO_POPOS: PoposPage[] = [];
const poposCache = new Map<string, { raw: string | null; pages: PoposPage[] }>();

export function readPopos(slug: string): PoposPage[] {
  let raw: string | null = null;
  try {
    raw = window.localStorage.getItem(poposKey(slug));
  } catch {
    raw = null;
  }
  const hit = poposCache.get(slug);
  if (hit && hit.raw === raw) return hit.pages;
  const pages: PoposPage[] = [];
  try {
    const list: unknown = raw ? JSON.parse(raw) : [];
    for (const item of Array.isArray(list) ? list : []) {
      const page = item as Partial<PoposPage>;
      const url = canonicalPageUrl(page?.url);
      if (!url || pages.some((p) => p.url === url)) continue;
      pages.push({
        id: pageIdForUrl(url),
        url,
        title: typeof page.title === "string" && page.title.trim() ? page.title.trim().slice(0, 60) : pageTitle(url),
      });
      if (pages.length >= MAX_POPOS) break;
    }
  } catch {
    pages.length = 0;
  }
  const result = pages.length ? pages : NO_POPOS;
  poposCache.set(slug, { raw, pages: result });
  return result;
}

function writePopos(slug: string, pages: PoposPage[]) {
  window.localStorage.setItem(poposKey(slug), JSON.stringify(pages));
  window.dispatchEvent(new Event(STORAGE_EVENT));
}

export function addPoposPage(slug: string, rawUrl: string, title?: string): AddCardResult {
  const url = canonicalPageUrl(rawUrl);
  if (!url) return "invalid";
  const pages = readPopos(slug);
  if (pages.some((page) => page.url === url)) return "exists";
  if (pages.length >= MAX_POPOS) return "full";
  writePopos(slug, [...pages, { id: pageIdForUrl(url), url, title: title?.trim().slice(0, 60) || pageTitle(url) }]);
  return "added";
}

export function usePopos(slug: string | null) {
  const pages = useSyncExternalStore(subscribe, () => (slug ? readPopos(slug) : NO_POPOS), () => NO_POPOS);
  const add = useCallback((url: string, title?: string) => (slug ? addPoposPage(slug, url, title) : "invalid" as const), [slug]);
  const remove = useCallback((id: string) => {
    if (slug) writePopos(slug, readPopos(slug).filter((page) => page.id !== id));
  }, [slug]);
  return { pages, add, remove };
}

// ---- Dashboard filter: ?source=popos,issue&issue=<id>,<id> ----

export interface DashboardFilter {
  sources: CardSource[];
  issueIds: string[];
}

const ISSUE_ID = /^[0-9a-zA-Z-]{1,64}$/;

export function parseDashboardFilter(params: URLSearchParams): DashboardFilter {
  const list = (name: string) => (params.get(name) ?? "").split(",").map((v) => v.trim()).filter(Boolean);
  return {
    sources: CARD_SOURCES.filter((source) => list("source").includes(source)),
    issueIds: [...new Set(list("issue").filter((id) => ISSUE_ID.test(id)))].slice(0, 50),
  };
}

export function dashboardFilterPath(workspaceSlug: string, filter: DashboardFilter): string {
  const params = new URLSearchParams();
  if (filter.sources.length) params.set("source", filter.sources.join(","));
  if (filter.issueIds.length) params.set("issue", filter.issueIds.join(","));
  const query = params.toString().replaceAll("%2C", ",");
  return query ? `${dashboardPath(workspaceSlug)}?${query}` : dashboardPath(workspaceSlug);
}

/**
 * Cards matching the filter: any selected source, and bound to any selected
 * issue (`linked` maps a card URL to the ids of the issues bound to it).
 */
export function filterCards(cards: DashboardCard[], filter: DashboardFilter, linked: Map<string, string[]>): DashboardCard[] {
  return cards.filter((card) =>
    (!filter.sources.length || filter.sources.includes(card.source))
    && (!filter.issueIds.length || (linked.get(card.url) ?? []).some((id) => filter.issueIds.includes(id))));
}
