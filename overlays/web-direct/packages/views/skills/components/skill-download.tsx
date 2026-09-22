"use client";

import { useRef, useState } from "react";
import { Download, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { skillDetailOptions } from "@multica/core/workspace/queries";
import { packStoreZip } from "@multica/core/skills";
import { Button } from "@multica/ui/components/ui/button";
import { DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";
import { toast } from "sonner";
import { useT } from "../../i18n";

export function SkillDownload({ skillId, wsId, menu = false }: {
  skillId: string;
  wsId: string;
  menu?: boolean;
}) {
  const { t } = useT("skills");
  const client = useQueryClient();
  const busy = useRef(false);
  const [downloading, setDownloading] = useState(false);
  async function download() {
    if (busy.current) return;
    busy.current = true;
    setDownloading(true);
    try {
      const skill = await client.fetchQuery({ ...skillDetailOptions(wsId, skillId), staleTime: 0 });
      const encoder = new TextEncoder();
      const paths = new Set(["SKILL.md"]);
      const entries = [{ path: "SKILL.md", data: encoder.encode(skill.content) }];
      for (const file of skill.files ?? []) {
        const path = file.path.replace(/\\/g, "/");
        if (!path || path.startsWith("/") || /^[a-z]:/i.test(path) || /[\x00-\x1f]/.test(path) || path.split("/").some((p) => !p || p === "." || p === "..") || paths.has(path)) {
          throw new Error("Invalid or duplicate skill file path");
        }
        paths.add(path);
        entries.push({ path, data: encoder.encode(file.content) });
      }
      const zip = packStoreZip(entries);
      const blob = new Blob([new Uint8Array(zip).buffer], { type: "application/zip" });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `${skill.name.replace(/[<>:"/\\|?*\x00-\x1f]/g, "_").replace(/[. ]+$/g, "") || "skill"}.zip`;
      document.body.appendChild(link);
      try { link.click(); } finally {
        link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 60_000);
      }
    } catch {
      toast.error(t(($) => $.actions.download_failed));
    } finally {
      busy.current = false;
      setDownloading(false);
    }
  }
  const content = <>{downloading ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />}{t(($) => $.actions.download)}</>;
  return menu
    ? <DropdownMenuItem disabled={downloading} onClick={() => void download()}>{content}</DropdownMenuItem>
    : <Button variant="outline" size="xs" disabled={downloading} onClick={() => void download()}>{content}</Button>;
}
