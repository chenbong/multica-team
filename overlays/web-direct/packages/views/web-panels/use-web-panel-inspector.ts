"use client";

import { useCallback, useLayoutEffect, useRef, type RefObject } from "react";
import type { Layout, PanelImperativeHandle, PanelSize } from "react-resizable-panels";
import { useResourceWebPanelContext } from "@multica/core/web-panels";

/** Keep temporary frame layout changes out of the task inspector preference. */
export function useWebPanelInspector({ panelRef, disabled, initiallyOpen, onLayoutChanged, onResize }: {
  panelRef: RefObject<PanelImperativeHandle | null>;
  disabled: boolean;
  initiallyOpen: boolean;
  onLayoutChanged: (layout: Layout) => void;
  onResize: (size: PanelSize) => void;
}) {
  const context = useResourceWebPanelContext();
  const store = context?.store;
  const suspended = useRef(false);
  const callbacks = useRef({ onLayoutChanged, onResize });
  callbacks.current = { onLayoutChanged, onResize };

  useLayoutEffect(() => {
    if (!store || disabled) return;
    let snapshot: { width: number; collapsed: boolean } | null = null;
    let firstFrame = 0;
    let secondFrame = 0;
    const cancelRestore = () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
    };
    const capture = (alreadyOpen: boolean) => {
      cancelRestore();
      // The store subscription runs before React hides the sizing wrapper.
      // On a newly mounted task with an already-open workspace frame, use
      // the task's initial preference instead of the hidden element's zero.
      snapshot ??= alreadyOpen
        ? { width: 320, collapsed: !initiallyOpen }
        : { width: panelRef.current?.getSize().inPixels ?? 320, collapsed: panelRef.current?.isCollapsed() ?? !initiallyOpen };
      suspended.current = true;
    };
    if (store.getState().activeKey) capture(true);
    const unsubscribe = store.subscribe((state, previous) => {
      if (state.activeKey && !previous.activeKey) capture(false);
      if (!state.activeKey && previous.activeKey && snapshot) {
        // Let the dock disappear and the parent's ResizeObserver settle
        // before restoring the saved pixel width against the full task page.
        firstFrame = window.requestAnimationFrame(() => {
          secondFrame = window.requestAnimationFrame(() => {
            const saved = snapshot;
            snapshot = null;
            suspended.current = false;
            if (!saved) return;
            if (saved.collapsed) panelRef.current?.collapse();
            else panelRef.current?.resize(`${saved.width}px`);
          });
        });
      }
    });
    return () => {
      unsubscribe();
      cancelRestore();
      suspended.current = false;
    };
  }, [store, disabled, panelRef, initiallyOpen]);

  return {
    onLayoutChanged: useCallback((layout: Layout) => {
      if (!suspended.current) callbacks.current.onLayoutChanged(layout);
    }, []),
    onResize: useCallback((size: PanelSize) => {
      if (!suspended.current) callbacks.current.onResize(size);
    }, []),
  };
}
