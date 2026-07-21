"use client";

import { STATUS_COLOR, STATUS_LABEL } from "@/components/UserPresence";
import { useSelfPresence } from "@/hooks/usePresence";
import { cn } from "@/lib/utils";

// The current user's own status toggle, in the sidebar footer. A connected
// user is only ever Online or Away (never Offline — they're right here using
// the app), so this is a two-state toggle: clicking flips between them and
// emits presence.update (see useSelfPresence). Offline is still shown for
// *other* users via UserPresence; it's just not a state you can pick for
// yourself. The chosen status drives your own presence dot everywhere
// (usePresence special-cases the current user) and is re-asserted on
// reconnect, so picking Away sticks even across a socket blip.
//
// Away is also set automatically by useAutoAway (hooks/useAutoAway.ts) after
// 5 minutes of inactivity. Clicking this control always overrides it: picking
// Online cancels the auto-away for the current idle window; picking Away sets
// it immediately (same end state the timer would have reached anyway).
export function SelfPresenceControl() {
  const { selfStatus, setSelfStatus } = useSelfPresence();
  const next = selfStatus === "online" ? "away" : "online";

  return (
    <button
      type="button"
      onClick={() => setSelfStatus(next)}
      title={
        next === "away"
          ? "Set yourself Away (also set automatically after 5 min of inactivity)"
          : "Set yourself Online"
      }
      className="mt-0.5 flex items-center gap-1.5 rounded text-xs text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      <span className={cn("h-2 w-2 shrink-0 rounded-full", STATUS_COLOR[selfStatus])} />
      {STATUS_LABEL[selfStatus]}
    </button>
  );
}
