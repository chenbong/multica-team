"use client";

import { use } from "react";
import { CustomPageView } from "@multica/views/layout";

export default function CustomPageViewRoute({
  searchParams,
}: {
  searchParams: Promise<{ url?: string | string[] }>;
}) {
  const { url } = use(searchParams);
  return <CustomPageView url={typeof url === "string" ? url : ""} />;
}
