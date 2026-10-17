"use client";

import { useRef, useState, type CSSProperties } from "react";
import { ExternalLink, RotateCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n";
import { PAGE_FRAME_SANDBOX } from "./custom-pages";

// A framed page that reloads itself this often is stuck in a loop (typically
// a login or cookie check that can never pass inside a cross-site frame).
export const RELOAD_LOOP_LOADS = 5;
export const RELOAD_LOOP_WINDOW_MS = 45_000;

/**
 * A cross-origin page inside Multica. Only the frame's `load` events are
 * observed (no messages, no DOM access); a reload loop is stopped and the page
 * is offered in a new tab instead.
 */
export function PageFrame({
  src,
  title,
  lazy,
  className,
  style,
}: {
  src: string;
  title: string;
  lazy?: boolean;
  className?: string;
  style?: CSSProperties;
}) {
  const { t } = useT("layout");
  const loads = useRef<number[]>([]);
  const [attempt, setAttempt] = useState(0);
  const [halted, setHalted] = useState(false);

  const onLoad = () => {
    const now = Date.now();
    loads.current = [...loads.current.filter((at) => now - at < RELOAD_LOOP_WINDOW_MS), now];
    if (loads.current.length >= RELOAD_LOOP_LOADS) setHalted(true);
  };
  const retry = () => {
    loads.current = [];
    setHalted(false);
    setAttempt((n) => n + 1);
  };

  if (halted) {
    return (
      <div role="alert" style={style} className={`flex flex-col items-center justify-center gap-3 p-6 text-center ${className ?? ""}`}>
        <p className="max-w-md text-sm text-muted-foreground">{t(($) => $.sidebar.custom_apps.reload_loop)}</p>
        <div className="flex gap-2">
          <Button type="button" size="sm" variant="outline" onClick={retry}>
            <RotateCw />
            {t(($) => $.sidebar.custom_apps.retry)}
          </Button>
          <Button size="sm" nativeButton={false} render={<a href={src} target="_blank" rel="noopener noreferrer" />}>
            <ExternalLink />
            {t(($) => $.sidebar.custom_apps.open_in_new_tab)}
          </Button>
        </div>
      </div>
    );
  }
  return (
    <iframe
      key={`${src}#${attempt}`}
      src={src}
      title={title}
      loading={lazy ? "lazy" : undefined}
      sandbox={PAGE_FRAME_SANDBOX}
      referrerPolicy="no-referrer"
      allow="clipboard-read; clipboard-write; fullscreen"
      onLoad={onLoad}
      style={style}
      className={`border-0 bg-background ${className ?? ""}`}
    />
  );
}
