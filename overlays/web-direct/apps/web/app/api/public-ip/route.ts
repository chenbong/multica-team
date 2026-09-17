import { lookup } from "node:dns/promises";
import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function configuredHostname(request: Request): string {
  const configured = process.env.MULTICA_HOST_IP?.trim();
  if (configured) return configured;

  try {
    return new URL(request.url).hostname;
  } catch {
    return "";
  }
}

function hostnameFrom(value: string): string {
  try {
    return new URL(value.includes("://") ? value : `http://${value}`).hostname;
  } catch {
    return value.split(":", 1)[0]?.trim() ?? "";
  }
}

function isIpv4(value: string): boolean {
  const parts = value.split(".");
  return parts.length === 4 && parts.every((part) => {
    const number = Number(part);
    return /^\d{1,3}$/.test(part) && number >= 0 && number <= 255;
  });
}

export async function GET(request: Request) {
  const hostname = hostnameFrom(configuredHostname(request));
  if (!hostname) {
    return NextResponse.json(
      { error: "No host configured for IP lookup" },
      { status: 503, headers: { "Cache-Control": "no-store" } },
    );
  }

  try {
    const ip = isIpv4(hostname)
      ? hostname
      : (await lookup(hostname, { family: 4 })).address;
    return NextResponse.json(
      { hostname, ip },
      { headers: { "Cache-Control": "no-store, max-age=0" } },
    );
  } catch {
    return NextResponse.json(
      { hostname, error: "DNS lookup failed" },
      { status: 503, headers: { "Cache-Control": "no-store" } },
    );
  }
}
