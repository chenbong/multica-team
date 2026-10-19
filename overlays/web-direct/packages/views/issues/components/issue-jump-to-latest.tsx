"use client";

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { ArrowDown } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

type Props = {
  scrollContainerEl: HTMLElement | null;
  composerRef: RefObject<HTMLElement | null>;
  stickyComposer: boolean;
  onPrepareJump: () => void;
};

export function IssueJumpToLatest({ scrollContainerEl, composerRef, stickyComposer, onPrepareJump }: Props) {
  const { t } = useT("issues");
  const [visible, setVisible] = useState(false);
  const [composerHeight, setComposerHeight] = useState(0);
  const stopJumpRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const container = scrollContainerEl;
    if (!container) return;
    let frame = 0;
    const viewport = window.visualViewport;
    const update = () => {
      const editing = document.activeElement?.closest("input, textarea, [contenteditable='true']");
      const keyboardOpen = Boolean(editing && viewport && window.innerHeight - viewport.height > 150);
      const remaining = container.scrollHeight - container.clientHeight - container.scrollTop;
      setVisible(!keyboardOpen && remaining > Math.max(160, container.clientHeight / 2));
      setComposerHeight(stickyComposer ? Math.ceil(composerRef.current?.getBoundingClientRect().height ?? 0) : 0);
    };
    const schedule = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(update);
    };
    update();
    const observer = new ResizeObserver(schedule);
    observer.observe(container);
    // The viewport's size stays constant while virtual rows or images grow.
    if (container.firstElementChild) observer.observe(container.firstElementChild);
    if (composerRef.current) observer.observe(composerRef.current);
    container.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    document.addEventListener("focusin", schedule);
    document.addEventListener("focusout", schedule);
    viewport?.addEventListener("resize", schedule);
    viewport?.addEventListener("scroll", schedule);
    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      container.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
      document.removeEventListener("focusin", schedule);
      document.removeEventListener("focusout", schedule);
      viewport?.removeEventListener("resize", schedule);
      viewport?.removeEventListener("scroll", schedule);
      stopJumpRef.current?.();
    };
  }, [scrollContainerEl, composerRef, stickyComposer]);

  const jump = useCallback(() => {
    const container = scrollContainerEl;
    if (!container) return;
    stopJumpRef.current?.();
    onPrepareJump();
    const startedAt = Date.now();
    let frame = 0;
    let stopped = false;
    const events = ["wheel", "touchstart", "pointerdown", "keydown"] as const;
    const stop = () => {
      stopped = true;
      cancelAnimationFrame(frame);
      for (const event of events) document.removeEventListener(event, stop, true);
      if (stopJumpRef.current === stop) stopJumpRef.current = null;
    };
    const settle = () => {
      if (stopped || !container.isConnected) return stop();
      // Only an explicit jump owns this short settling window. Never enable
      // Virtuoso followOutput or scroll any ancestor of the issue viewport.
      container.scrollTop = Math.max(0, container.scrollHeight - container.clientHeight);
      container.dispatchEvent(new Event("scroll"));
      if (Date.now() - startedAt < 2000) frame = requestAnimationFrame(settle);
      else stop();
    };
    stopJumpRef.current = stop;
    for (const event of events) document.addEventListener(event, stop, { capture: true, passive: true });
    settle();
  }, [onPrepareJump, scrollContainerEl]);

  if (!visible || !scrollContainerEl) return null;
  return (
    <Button
      type="button"
      variant="outline"
      size="icon-lg"
      aria-label={t(($) => $.detail.jump_to_latest)}
      title={t(($) => $.detail.jump_to_latest)}
      className="absolute right-4 z-20 min-h-11 min-w-11 rounded-full bg-background shadow-md md:right-10"
      style={{ bottom: `calc(max(1rem, env(safe-area-inset-bottom, 0px)) + max(var(--chat-launcher-clearance, 0px), ${composerHeight}px))` }}
      onClick={jump}
    >
      <ArrowDown aria-hidden="true" />
    </Button>
  );
}
