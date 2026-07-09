"use client";

import { Menu } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useUIStore } from "@/lib/store";

// Opens the mobile sidebar drawer (see app/chat/layout.tsx). Rendered from
// RoomHeader and the /chat empty state — the two screens that have no
// static sidebar column to fall back on below the md breakpoint.
export function MobileSidebarTrigger() {
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen);

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="shrink-0 md:hidden"
      aria-label="Open rooms menu"
      onClick={() => setSidebarOpen(true)}
    >
      <Menu className="h-5 w-5" />
    </Button>
  );
}
