"use client";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FolderOpen } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { projectResourcesOptions, useCreateProjectResource, useUpdateProjectResource, useDeleteProjectResource } from "@multica/core/projects";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";

const absolutePathExample = "/home/user/project";

export function RemoteWorkingDirectory({ projectId }: { projectId: string }) {
 const { t } = useT("projects");
 const wsId = useWorkspaceId();
 const { data: resources = [] } = useQuery(projectResourcesOptions(wsId, projectId));
 const current = resources.find(r => r.resource_type === "working_directory");
 const currentPath = current && "local_path" in current.resource_ref && typeof current.resource_ref.local_path === "string" ? current.resource_ref.local_path : "";
 const legacy = resources.some(r => r.resource_type === "local_directory");
 const create = useCreateProjectResource(wsId, projectId);
 const update = useUpdateProjectResource(wsId, projectId);
 const remove = useDeleteProjectResource(wsId, projectId);
 const [open, setOpen] = useState(false);
 const [path, setPath] = useState("");
 const [error, setError] = useState("");
 const pending = create.isPending || update.isPending || remove.isPending;
 const value = path.trim();
 const valid = /^(?:\/|\\\\|[A-Za-z]:[\\/])/.test(value) && !value.includes("\0");
 const save = async () => {
  if (!valid || pending) return;
  setError("");
  try {
   const resource_ref = { local_path: value };
   if (current) await update.mutateAsync({ resourceId: current.id, data: { resource_ref } });
   else await create.mutateAsync({ resource_type: "working_directory", resource_ref });
   setOpen(false);
  } catch (e) { setError(e instanceof Error ? e.message : t($ => $.remote_directory.failed)); }
 };
 const clear = async () => {
  if (!current || pending) return;
  setError("");
  try { await remove.mutateAsync(current.id); setOpen(false); }
  catch (e) { setError(e instanceof Error ? e.message : t($ => $.remote_directory.failed)); }
 };
 return <Dialog open={open} onOpenChange={v => { if (!pending) { setOpen(v); setError(""); if (v) setPath(currentPath); } }}>
  <DialogTrigger render={<Button variant="ghost" size="sm" className="h-auto justify-start px-2 text-caption text-muted-foreground" />}><FolderOpen className="size-3 shrink-0" /><span className="min-w-0 truncate" title={currentPath || undefined}>{currentPath || t($ => $.remote_directory.title)}</span></DialogTrigger>
  <DialogContent><DialogHeader><DialogTitle>{t($ => $.remote_directory.title)}</DialogTitle><DialogDescription>{t($ => $.remote_directory.hint)}</DialogDescription></DialogHeader>
   <div className="space-y-3">
    <div className="space-y-2"><Label htmlFor="project-working-path">{t($ => $.remote_directory.path)}</Label><Input id="project-working-path" value={path} disabled={pending} placeholder={absolutePathExample} onChange={e => setPath(e.target.value)} /></div>
    <p className="text-caption text-muted-foreground">{t($ => $.remote_directory.compatibility)}</p>
    {legacy && <p className="text-caption text-muted-foreground">{t($ => $.remote_directory.legacy)}</p>}
    {error && <p role="alert" className="text-caption text-destructive">{error}</p>}
   </div>
   <DialogFooter>
    {current && <Button variant="outline" disabled={pending} onClick={() => void clear()}>{t($ => $.remote_directory.remove)}</Button>}
    <Button variant="outline" onClick={() => setOpen(false)} disabled={pending}>{t($ => $.remote_directory.cancel)}</Button><Button onClick={() => void save()} disabled={!valid || pending} aria-busy={pending}>{t($ => $.remote_directory.save)}</Button>
   </DialogFooter>
  </DialogContent>
 </Dialog>;
}
