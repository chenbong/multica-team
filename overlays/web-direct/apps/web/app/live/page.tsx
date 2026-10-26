import { notFound } from "next/navigation";
import { LiveDashboardPreview } from "./preview";

export const metadata = { title: "平台实时看板" };
export const dynamic = "force-dynamic";

export default function LivePage() {
  // Demo numbers must never be a production fallback or URL-controlled mode.
  if (process.env.TEAM_MESH_LIVE_PREVIEW !== "true" && process.env.MULTICA_PUBLIC_LIVE_ENABLED !== "true") notFound();
  const quickStartHref = process.env.MULTICA_QUICKSTART_URL;
  return <LiveDashboardPreview readOnlyPreview={process.env.TEAM_MESH_LIVE_READONLY === "true"} mode={process.env.TEAM_MESH_LIVE_READONLY === "true" || process.env.MULTICA_PUBLIC_LIVE_ENABLED === "true" ? "live" : "demo"} quickStartHref={quickStartHref && /^https?:\/\//.test(quickStartHref) ? quickStartHref : undefined} />;
}
