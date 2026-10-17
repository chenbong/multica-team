"use client";

import { useState, type FormEvent } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { useWorkspaceSlug } from "@multica/core/paths";
import { useT } from "../i18n";
import { MAX_DASHBOARD_CARDS, MAX_POPOS, frameableUrl, useDashboard, usePopos } from "./custom-pages";

/** Adds a pasted page link to POPOs or to the dashboard (both in this browser only). */
export function AddPageDialog({
  target,
  open,
  onOpenChange,
  onAdded,
}: {
  target: "popos" | "dashboard";
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded?: (url: string) => void;
}) {
  const { t } = useT("layout");
  const slug = useWorkspaceSlug();
  const dashboard = useDashboard(slug);
  const popos = usePopos(slug);
  const toPopos = target === "popos";
  const add = (rawUrl: string, rawTitle?: string) =>
    toPopos ? popos.add(rawUrl, rawTitle) : dashboard.add(rawUrl, rawTitle, "manual");
  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState<string | null>(null);

  const submit = (rawUrl: string, rawTitle?: string) => {
    if (!frameableUrl(rawUrl)) {
      setError(t(($) => $.sidebar.custom_apps.invalid_url));
      return;
    }
    const result = add(rawUrl, rawTitle);
    if (result === "added") {
      setUrl("");
      setTitle("");
      setError(null);
      onOpenChange(false);
      onAdded?.(frameableUrl(rawUrl)!);
    } else if (result === "exists") {
      setError(toPopos ? t(($) => $.sidebar.custom_apps.popos_exists) : t(($) => $.sidebar.custom_apps.exists));
    } else if (result === "full") {
      setError(toPopos
        ? t(($) => $.sidebar.custom_apps.popos_full, { count: MAX_POPOS })
        : t(($) => $.sidebar.custom_apps.full, { count: MAX_DASHBOARD_CARDS }));
    } else {
      setError(t(($) => $.sidebar.custom_apps.invalid_url));
    }
  };
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    submit(url, title);
  };

  return (
    <Dialog open={open} onOpenChange={(next) => { setError(null); onOpenChange(next); }}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{toPopos ? t(($) => $.sidebar.custom_apps.popos_add) : t(($) => $.sidebar.custom_apps.add_page)}</DialogTitle>
            <DialogDescription>
              {toPopos ? t(($) => $.sidebar.custom_apps.popos_add_hint) : t(($) => $.sidebar.custom_apps.add_page_hint)}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="custom-page-url">{t(($) => $.sidebar.custom_apps.url_label)}</Label>
            <Input id="custom-page-url" type="text" inputMode="url" autoComplete="off" spellCheck={false} required autoFocus
              value={url} placeholder={t(($) => $.sidebar.custom_apps.url_placeholder)}
              onChange={(event) => setUrl(event.target.value)} aria-invalid={error ? true : undefined} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="custom-page-title">{t(($) => $.sidebar.custom_apps.title_label)}</Label>
            <Input id="custom-page-title" value={title} maxLength={60} onChange={(event) => setTitle(event.target.value)} />
          </div>
          {error ? <p role="alert" className="text-caption text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t(($) => $.sidebar.custom_apps.cancel)}
            </Button>
            <Button type="submit">{t(($) => $.sidebar.custom_apps.add)}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
