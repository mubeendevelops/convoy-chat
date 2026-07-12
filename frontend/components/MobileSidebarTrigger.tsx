"use client";

import { Menu } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useUnreadTotal } from "@/hooks/useRooms";
import { useUIStore } from "@/lib/store";

// Opens the mobile sidebar drawer (see app/chat/layout.tsx). Rendered from
// RoomHeader and the /chat empty state — the two screens that have no
// static sidebar column to fall back on below the md breakpoint. Carries a
// total-unread count badge so a new message in any room is visible while the
// drawer is closed.
export function MobileSidebarTrigger() {
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen);
  const unreadTotal = useUnreadTotal();

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="relative shrink-0 md:hidden"
      aria-label={unreadTotal > 0 ? `Open rooms menu, ${unreadTotal} unread` : "Open rooms menu"}
      onClick={() => setSidebarOpen(true)}
    >
      <Menu className="h-5 w-5" />
      {unreadTotal > 0 && (
        <span className="absolute right-1 top-1 inline-flex h-4 min-w-[1rem] items-center justify-center rounded-full bg-primary px-1 text-[0.625rem] font-medium leading-none text-primary-foreground">
          {unreadTotal > 99 ? "99+" : unreadTotal}
        </span>
      )}
    </Button>
  );
}
