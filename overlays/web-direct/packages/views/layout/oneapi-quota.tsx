"use client";
import { useAuthStore } from "@multica/core/auth";
import { useOneAPIQuota } from "@multica/core/runtimes";
import { useT } from "../i18n";

export function OneAPIQuota({ href }: { href: string }) {
  const userId = useAuthStore((state) => state.user?.id);
  const query = useOneAPIQuota(userId);
  const { t } = useT("layout");
  const data = query.data;
  const values = data?.values;
  const known = (data?.status === "ready" || data?.status === "stale") && values;
  const unlimited = known && values.unlimited;
  const numeric = known && typeof values.used === "number" && typeof values.total === "number";
  const number = (v: number) => v.toLocaleString(undefined, { maximumFractionDigits: 2 });
  const text = unlimited ? t($ => $.oneapi_quota.unlimited)
    : numeric ? `${number(values.used!)} / ${number(values.total!)}`
    : data?.status === "not_linked" ? t($ => $.oneapi_quota.not_linked)
    : data?.status === "credential_invalid" ? t($ => $.oneapi_quota.invalid)
    : query.isPending ? t($ => $.oneapi_quota.loading) : t($ => $.oneapi_quota.unavailable);
  const stale = !!known && (query.isError || data?.status === "stale");
  const detail = ["OneAPI", known && typeof values.remaining === "number" ? t($ => $.oneapi_quota.remaining, { value: number(values.remaining) }) : "", stale ? t($ => $.oneapi_quota.stale) : "", data?.updatedAt ? new Date(data.updatedAt).toLocaleString() : "", ["not_linked", "credential_invalid"].includes(data?.status ?? "") ? t($ => $.oneapi_quota.import_hint) : ""].filter(Boolean).join(" · ");
  const percent = numeric && values.total! > 0 ? Math.min(100, Math.max(0, values.used! / values.total! * 100)) : 0;
  if (data?.status === "disabled") return null;
  return <a href={href} target="_blank" rel="noopener noreferrer" className="block w-full min-w-0 rounded-md px-2 py-1.5 text-left text-caption group-data-[collapsible=icon]:hidden" title={detail} aria-label={t($ => $.oneapi_quota.accessible, {value: `${text}${stale ? ` · ${t($ => $.oneapi_quota.stale)}` : ""}`})}>
    <span className="flex items-start justify-between gap-2"><span className="shrink-0 text-muted-foreground">{t($ => $.oneapi_quota.label)}</span><span className="min-w-0 break-words text-right tabular-nums">{text}</span></span>
    {numeric && !unlimited && <span role="progressbar" aria-label={t($ => $.oneapi_quota.label)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} className="mt-1.5 block h-1.5 w-full overflow-hidden rounded-full border border-border bg-muted-foreground/20"><span className="block h-full rounded-full bg-primary transition-[width] duration-500 ease-out motion-reduce:transition-none" style={{ width: `${percent}%` }} /></span>}
    {stale && <span className="text-muted-foreground">{t($ => $.oneapi_quota.stale)}</span>}
  </a>;
}
