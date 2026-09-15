"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { SkillSummary, Workspace } from "@multica/core/types";
import { api } from "@multica/core/api";
import { workspaceKeys, workspaceListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";

export type SkillScope = "personal" | "workspace" | "team";

export function SkillScopeDialog({
  skill,
  wsId,
  open,
  onOpenChange,
}: {
  skill: SkillSummary;
  wsId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("skills");
  const qc = useQueryClient();
  const { data: workspaces = [] } = useQuery(workspaceListOptions());
  const [scope, setScope] = useState<SkillScope>(skill.scope ?? "workspace");
  const [workspaceId, setWorkspaceId] = useState(skill.workspace_id || wsId);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setScope(skill.scope ?? "workspace");
    setWorkspaceId(skill.workspace_id || wsId);
  }, [open, skill.scope, skill.workspace_id, wsId]);

  const handleSave = async () => {
    setSaving(true);
    try {
      await api.changeSkillScope(skill.id, {
        scope,
        ...(scope === "team" ? {} : { workspace_id: workspaceId || wsId }),
      });
      await qc.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) });
      toast.success(t(($) => $.actions.change_level_saved));
      onOpenChange(false);
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.actions.change_level_failed),
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(value) => !saving && onOpenChange(value)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t(($) => $.actions.change_level_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.actions.change_level_desc)}</DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          {(["team", "workspace", "personal"] as const).map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={scope === value}
              onClick={() => setScope(value)}
              className={`flex w-full items-start gap-3 rounded-lg border p-3 text-left transition-colors ${scope === value ? "border-primary bg-accent/40" : "hover:bg-accent/30"}`}
            >
              <span className={`mt-0.5 size-3 rounded-full border-2 ${scope === value ? "border-primary bg-primary" : "border-muted-foreground"}`} />
              <span className="min-w-0 flex-1">
                <span className="block text-body font-medium">{t(($) => $.create.scope[value].title)}</span>
                <span className="mt-0.5 block text-caption text-muted-foreground">{t(($) => $.create.scope[value].desc)}</span>
              </span>
            </button>
          ))}
        </div>

        {scope === "workspace" && (
          <div className="space-y-1.5">
            <Label htmlFor="skill-scope-workspace">{t(($) => $.table.level_project)}</Label>
            <select
              id="skill-scope-workspace"
              value={workspaceId}
              onChange={(event) => setWorkspaceId(event.target.value)}
              className="h-9 w-full rounded-md border bg-background px-2 text-body"
            >
              {workspaces.map((workspace: Workspace) => (
                <option key={workspace.id} value={workspace.id}>
                  {workspace.name}
                </option>
              ))}
              {workspaces.length === 0 && <option value={wsId}>{skill.workspace_name || t(($) => $.table.level_current_project)}</option>}
            </select>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)} disabled={saving}>
            {t(($) => $.actions.cancel)}
          </Button>
          <Button type="button" onClick={handleSave} disabled={saving || (scope === "workspace" && !workspaceId)}>
            {t(($) => $.actions.change_level_save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
