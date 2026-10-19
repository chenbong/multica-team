"use client";

import { useId, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, Pencil, Plus, X } from "lucide-react";
import { errorCode } from "@multica/core/api";
import {
  useResourceWebPanelContext, useResourceWebPanelState, useWebPanelMutations, webPanelListOptions, webPanelUrl, webPanelReferenceKey,
  type ResourceWebPanel, type WebPanelInput, type WebPanelResource,
} from "@multica/core/web-panels";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { SidebarGroup, SidebarGroupContent, SidebarGroupLabel } from "@multica/ui/components/ui/sidebar";
import { useT } from "../i18n";

export function WorkspaceWebPanelsNav() {
  const context = useResourceWebPanelContext();
  if (!context) return null;
  return <SidebarGroup><SidebarGroupLabel><WorkspacePanelHeading /></SidebarGroupLabel><SidebarGroupContent><ResourceWebPanelList resource={{ scope: "workspace", resourceId: context.queryContext.workspaceId }} /></SidebarGroupContent></SidebarGroup>;
}

function WorkspacePanelHeading() {
  const { t } = useT("layout");
  return t(($) => $.sidebar.web_panels.workspace_title);
}

export function IssueWebPanelsSection({ issueId }: { issueId: string }) {
  const { t } = useT("layout");
  const context = useResourceWebPanelContext();
  if (!context) return null;
  return <section aria-label={t(($) => $.sidebar.web_panels.issue_title)}><h3 className="mb-2 px-2 text-caption font-medium">{t(($) => $.sidebar.web_panels.issue_title)}</h3><ResourceWebPanelList resource={{ scope: "issue", resourceId: issueId }} /></section>;
}

export function ResourceWebPanelList({ resource }: { resource: WebPanelResource }) {
  const { t } = useT("layout");
  const { queryContext, store } = useResourceWebPanelContext()!;
  const activeKey = useResourceWebPanelState((state) => state.activeKey);
  const query = useQuery(webPanelListOptions(queryContext, resource));
  const mutations = useWebPanelMutations(queryContext, resource);
  const [editing, setEditing] = useState<ResourceWebPanel | "new" | null>(null);
  const [removing, setRemoving] = useState<ResourceWebPanel | null>(null);
  const [removeError, setRemoveError] = useState(false);
  const panels = query.data?.panels ?? [];
  const canAdd = !query.isError && query.data?.canAdd === true && panels.length < query.data.limit;
  const submit = async (input: WebPanelInput) => {
    if (editing === "new") await mutations.create.mutateAsync(input);
    else if (editing) await mutations.update.mutateAsync({ panelId: editing.id, input });
  };

  return (
    <div className="flex flex-col gap-1 px-1">
      {query.isPending ? <p role="status" className="px-2 text-caption text-muted-foreground">{t(($) => $.sidebar.web_panels.loading)}</p> : null}
      {query.isError ? <div role="alert" className="px-2 text-caption text-destructive">{t(($) => $.sidebar.web_panels.load_failed)} <Button size="xs" variant="ghost" onClick={() => void query.refetch()}>{t(($) => $.sidebar.web_panels.retry)}</Button></div> : null}
      {!query.isError && query.data && panels.length === 0 ? <p className="px-2 text-caption text-muted-foreground">{t(($) => $.sidebar.web_panels.empty)}</p> : null}
      {!query.isError ? <ul className="flex flex-col gap-0.5">
        {panels.map((panel) => (
          <li key={panel.id} className="flex min-w-0 items-center gap-0.5 rounded-md hover:bg-accent/50">
            <Button type="button" size="sm" variant="ghost" title={panel.url} aria-pressed={activeKey === webPanelReferenceKey({ scope: panel.scope, resourceId: panel.resourceId, panelId: panel.id })} className={`min-w-0 flex-1 justify-start truncate font-normal ${activeKey === webPanelReferenceKey({ scope: panel.scope, resourceId: panel.resourceId, panelId: panel.id }) ? "bg-accent text-accent-foreground" : ""}`}
              onClick={() => store.getState().open({ scope: panel.scope, resourceId: panel.resourceId, panelId: panel.id })}><span className="truncate">{panel.name}</span></Button>
            <a className={buttonVariants({ size: "icon-xs", variant: "ghost" })} aria-label={`${t(($) => $.sidebar.web_panels.open_new_tab)} ${panel.name}`} href={panel.url} target="_blank" rel="noopener noreferrer"><ExternalLink /></a>
            {panel.canManage ? <>
              <Button type="button" size="icon-xs" variant="ghost" aria-label={`${t(($) => $.sidebar.web_panels.edit)} ${panel.name}`} onClick={() => setEditing(panel)}><Pencil /></Button>
              <Button type="button" size="icon-xs" variant="ghost" aria-label={`${t(($) => $.sidebar.web_panels.remove)} ${panel.name}`} onClick={() => { setRemoveError(false); setRemoving(panel); }}><X /></Button>
            </> : null}
          </li>
        ))}
      </ul> : null}
      {query.data && panels.length >= query.data.limit ? <p className="px-2 text-caption text-muted-foreground">{t(($) => $.sidebar.web_panels.limit, { count: query.data.limit })}</p> : null}
      {canAdd ? <Button type="button" size="sm" variant="ghost" className="justify-start text-muted-foreground" onClick={() => setEditing("new")}><Plus />{t(($) => $.sidebar.web_panels.add)}</Button> : null}
      {editing ? <WebPanelEditor key={editing === "new" ? "new" : editing.id} panel={editing === "new" ? null : editing} submit={submit} onClose={() => setEditing(null)} /> : null}
      <Dialog open={removing !== null} onOpenChange={(open) => { if (!open && !mutations.remove.isPending) setRemoving(null); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t(($) => $.sidebar.web_panels.remove_title)}</DialogTitle><DialogDescription>{t(($) => $.sidebar.web_panels.remove_hint)}</DialogDescription></DialogHeader>
          {removing ? <p className="break-all text-sm">{removing.name}</p> : null}
          {removeError ? <p role="alert" className="text-sm text-destructive">{t(($) => $.sidebar.web_panels.remove_failed)}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={mutations.remove.isPending} onClick={() => setRemoving(null)}>{t(($) => $.sidebar.web_panels.cancel)}</Button>
            <Button type="button" variant="destructive" disabled={mutations.remove.isPending} aria-busy={mutations.remove.isPending}
              onClick={async () => {
                if (!removing) return;
                try { await mutations.remove.mutateAsync(removing.id); store.getState().forget(removing.id); setRemoving(null); }
                catch { setRemoveError(true); }
              }}>{t(($) => $.sidebar.web_panels.remove)}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function WebPanelEditor({ panel, submit, onClose }: { panel: ResourceWebPanel | null; submit: (input: WebPanelInput) => Promise<void>; onClose: () => void }) {
  const { t } = useT("layout");
  const id = useId();
  const [name, setName] = useState(panel?.name === panel?.url ? "" : panel?.name ?? "");
  const [url, setUrl] = useState(panel?.url ?? "");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async (event: FormEvent) => {
    event.preventDefault();
    const href = webPanelUrl(url);
    if (!href) { setError(t(($) => $.sidebar.web_panels.invalid_url)); return; }
    if (name.trim() !== href && [...name.trim()].length > 120) { setError(t(($) => $.sidebar.web_panels.invalid_name)); return; }
    setPending(true);
    setError(null);
    try { await submit({ name: name.trim(), url: href }); onClose(); }
    catch (failure) { setError(errorCode(failure) === "web_panel_exists" ? t(($) => $.sidebar.web_panels.exists) : t(($) => $.sidebar.web_panels.save_failed)); }
    finally { setPending(false); }
  };
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !pending) onClose(); }}>
      <DialogContent>
        <form onSubmit={save} className="flex flex-col gap-4">
          <DialogHeader><DialogTitle>{panel ? t(($) => $.sidebar.web_panels.edit) : t(($) => $.sidebar.web_panels.add)}</DialogTitle><DialogDescription>{t(($) => $.sidebar.web_panels.permission_hint)}</DialogDescription></DialogHeader>
          <div className="flex flex-col gap-1.5"><Label htmlFor={`${id}-url`}>{t(($) => $.sidebar.web_panels.url)}</Label><Input id={`${id}-url`} autoFocus type="url" inputMode="url" autoComplete="off" spellCheck={false} required value={url} onChange={(event) => setUrl(event.target.value)} disabled={pending} /></div>
          <div className="flex flex-col gap-1.5"><Label htmlFor={`${id}-name`}>{t(($) => $.sidebar.web_panels.name)}</Label><Input id={`${id}-name`} placeholder={t(($) => $.sidebar.web_panels.name_placeholder)} value={name} onChange={(event) => setName(event.target.value)} disabled={pending} /></div>
          {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
          <DialogFooter><Button type="button" variant="outline" disabled={pending} onClick={onClose}>{t(($) => $.sidebar.web_panels.cancel)}</Button><Button type="submit" disabled={pending} aria-busy={pending}>{t(($) => $.sidebar.web_panels.save)}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
