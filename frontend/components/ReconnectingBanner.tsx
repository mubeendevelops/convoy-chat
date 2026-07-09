"use client";

import { WifiOff } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useWebSocket } from "@/hooks/useWebSocket";

// Only shown once the socket has dropped after an earlier successful
// connect (see useWebSocket's hasConnectedOnce) — the brief "connecting"
// window on first page load isn't a reconnect and shouldn't alarm anyone.
// Reconnection itself is already automatic (backoff + jitter); the button
// here just lets an impatient user skip the remaining wait rather than
// being the only way to recover.
export function ReconnectingBanner() {
  const { status, hasConnectedOnce, reconnectNow } = useWebSocket();

  if (status === "open" || !hasConnectedOnce) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      // relative + z-[110]: shadcn's stock ToastViewport pins toasts to
      // top-0 below the sm: breakpoint (only moving to bottom-right at
      // sm+), which can overlap this banner — also top-of-screen — on a
      // phone-width layout. Found via Phase 15's mobile smoke test: a
      // failed-send toast sat on top of this banner's own "Reconnect now"
      // button and ate its clicks. Static elements ignore z-index, hence
      // `relative` here too.
      className="relative z-[110] flex shrink-0 items-center justify-center gap-3 border-b bg-amber-500/15 px-4 py-2 text-sm text-amber-700 dark:text-amber-400"
    >
      <WifiOff className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span>Reconnecting…</span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-7 border-amber-600/40 bg-transparent px-2 text-xs text-amber-700 hover:bg-amber-500/10 dark:text-amber-400"
        onClick={reconnectNow}
      >
        Reconnect now
      </Button>
    </div>
  );
}
