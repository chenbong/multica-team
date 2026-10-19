"use client";

import { useCallback, useEffect, useRef, useState } from "react";

const LOAD_TIMEOUT_MS = 20_000;
const NAVIGATION_WINDOW_MS = 15_000;
const MAX_NAVIGATIONS = 5;
type Failure = "timeout" | "loop" | "error" | "restricted" | "manual";

/** Stop the browsing context, not just its spinner, on a bounded failure. */
export function useWebPanelLoadGuard(url: string, enabled: boolean) {
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<Failure | null>(null);
  const stopped = useRef(false);
  const loaded = useRef(false);
  const navigations = useRef<number[]>([]);
  const frameRef = useRef<HTMLIFrameElement | null>(null);
  const stop = useCallback((reason: Failure) => {
    if (stopped.current) return;
    stopped.current = true;
    setFailure(reason);
    setLoading(false);
  }, []);

  useEffect(() => {
    if (!enabled || !loading || failure) return;
    const timer = window.setTimeout(() => { if (!loaded.current) stop("timeout"); }, LOAD_TIMEOUT_MS);
    return () => window.clearTimeout(timer);
  }, [enabled, loading, failure, stop]);

  useEffect(() => {
    if (!enabled || failure) return;
    // Only the parent's enforced policy is observable here. A remote site's
    // frame-ancestors denial can still fire load without an error event.
    const onViolation = (event: SecurityPolicyViolationEvent) => {
      if (event.disposition !== "enforce" || !["frame-src", "child-src"].includes(event.effectiveDirective)) return;
      try {
        if (new URL(event.blockedURI).origin === new URL(url).origin) stop("restricted");
      } catch { /* Non-URL policy violations do not identify this frame. */ }
    };
    document.addEventListener("securitypolicyviolation", onViolation);
    // iframe error events are not consistently delegated by React. Browsers
    // may suppress the event altogether, so it is only an additional signal.
    const frame = frameRef.current;
    const onError = () => stop("error");
    frame?.addEventListener("error", onError);
    return () => {
      document.removeEventListener("securitypolicyviolation", onViolation);
      frame?.removeEventListener("error", onError);
    };
  }, [enabled, failure, stop, url]);

  const onLoad = useCallback(() => {
    if (stopped.current) return;
    const now = Date.now();
    navigations.current = [...navigations.current.filter((time) => now - time <= NAVIGATION_WINDOW_MS), now];
    if (navigations.current.length >= MAX_NAVIGATIONS) {
      stop("loop");
      return;
    }
    // load is not proof that cross-origin content is visible. Keep a manual
    // escape hatch instead of reading protected DOM or timing out healthy pages.
    loaded.current = true;
    setLoading(false);
  }, [stop]);

  return { frameRef, loading, failure, onLoad, stop };
}
