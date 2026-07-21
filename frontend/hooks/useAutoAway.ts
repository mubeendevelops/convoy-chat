"use client";

import { useEffect, useRef } from "react";

import { useSelfPresence } from "@/hooks/usePresence";
import { usePresenceStore } from "@/lib/presence-store";

// How long the user must be idle before being automatically marked Away.
// 5 minutes matches common chat-app conventions (Slack, Teams) and gives
// enough margin that a brief focus-switch doesn't flip the indicator.
const INACTIVITY_TIMEOUT_MS = 5 * 60 * 1000;

// How often the inactivity timer can be reset while activity events are
// firing continuously (e.g. every pixel of mouse movement). Prevents a
// flood of clearTimeout/setTimeout calls while still restarting the
// countdown promptly after genuine activity.
const THROTTLE_MS = 1_000;

// DOM events that count as user activity. Attached in the capturing phase
// so a single listener at the document root catches events from any element,
// even those that call stopPropagation during the bubbling phase.
const ACTIVITY_EVENTS = [
  "mousemove",
  "mousedown",
  "keydown",
  "scroll",
  "touchstart",
  "wheel",
] as const;

/**
 * Automatically transitions the current user's presence between "online"
 * and "away" based on input inactivity:
 *
 *   online → away    after INACTIVITY_TIMEOUT_MS with no activity.
 *   away   → online  immediately on the first activity event after going away.
 *
 * Reuses the exact same `presence.update` WebSocket event and `selfStatus`
 * store field that SelfPresenceControl's manual toggle uses — reconnect
 * re-assertion, peer visibility, and dot rendering all behave identically
 * regardless of whether the status was set manually or automatically.
 *
 * Zero re-renders: all timer and throttle state lives in refs. The only
 * React state mutations that occur are the ones setSelfStatus already
 * performs (presence store write + WS send), which happen on a manual
 * toggle too.
 *
 * Must be mounted inside WebSocketProvider (useAutoAway → useSelfPresence →
 * useWebSocket). Use the thin AutoAwayEffect wrapper component defined in
 * app/chat/layout.tsx rather than calling this hook directly there, since
 * ChatLayout renders WebSocketProvider and therefore cannot call hooks that
 * depend on it.
 *
 * Multiple-tab note: each tab manages its own socket and selfStatus
 * (the store is in-memory, not persisted). Activity in one tab does not
 * suppress the away timer in another. This is consistent with the backend's
 * connection-count model: the tab with the most recent activity wins the
 * last-write-wins Redis key — correct behavior at this scale.
 */
export function useAutoAway(inactivityMs = INACTIVITY_TIMEOUT_MS): void {
  const { setSelfStatus } = useSelfPresence();

  // Keep the latest setSelfStatus in a ref so the stable event-listener
  // callback always calls the current version without needing to be
  // recreated (and therefore without having to re-attach listeners).
  // setSelfStatus is already effectively stable (its own useCallback deps
  // are stable), but the ref guards against any future change in that.
  const setSelfStatusRef = useRef(setSelfStatus);
  useEffect(() => {
    setSelfStatusRef.current = setSelfStatus;
  }, [setSelfStatus]);

  useEffect(() => {
    // Use a plain object rather than useRef here because this timer is
    // entirely internal to the effect's closure — it doesn't need to be
    // read from outside, and the cleanup function captures it correctly.
    const timer = { id: null as ReturnType<typeof setTimeout> | null };
    let lastResetAt = 0;

    function scheduleAway() {
      if (timer.id !== null) clearTimeout(timer.id);
      timer.id = setTimeout(() => {
        // Only transition online → away. If the user already set themselves
        // away manually we leave it alone — no extra WS event, no flicker.
        if (usePresenceStore.getState().selfStatus !== "away") {
          setSelfStatusRef.current("away");
        }
      }, inactivityMs);
    }

    function onActivity() {
      // Immediately recover away → online on any input, regardless of
      // throttle. The status update is idempotent when already online, so
      // this extra check is the only cost for the common case.
      if (usePresenceStore.getState().selfStatus === "away") {
        setSelfStatusRef.current("online");
      }

      // Throttle timer resets: rapid events (mouse movement, scroll) would
      // otherwise spam clearTimeout/setTimeout on every frame. We still
      // restart the countdown at least once per THROTTLE_MS of continuous
      // activity, which is accurate enough for a 5-minute idle window.
      const now = Date.now();
      if (now - lastResetAt < THROTTLE_MS) return;
      lastResetAt = now;

      scheduleAway();
    }

    for (const eventName of ACTIVITY_EVENTS) {
      document.addEventListener(eventName, onActivity, {
        capture: true,
        // passive: true is safe here — we never call preventDefault on
        // these events, and it lets the browser optimize scroll/touch perf.
        passive: true,
      });
    }

    // Start the initial countdown from mount.
    scheduleAway();

    return () => {
      for (const eventName of ACTIVITY_EVENTS) {
        document.removeEventListener(eventName, onActivity, { capture: true });
      }
      if (timer.id !== null) clearTimeout(timer.id);
    };
  }, [inactivityMs]);
  // Only re-runs if inactivityMs changes, which it never does in practice
  // (the default constant is used throughout). setSelfStatus is accessed
  // via setSelfStatusRef, not as a dep, to keep the listener stable.
}
