"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";

import { ChatSidebarContent } from "@/components/ChatSidebar";
import { ReconnectingBanner } from "@/components/ReconnectingBanner";
import { UnreadTitle } from "@/components/UnreadTitle";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { useAutoAway } from "@/hooks/useAutoAway";
import { useRequireAuth } from "@/hooks/useAuth";
import { WebSocketProvider } from "@/hooks/useWebSocket";
import { useUIStore } from "@/lib/store";

// Thin wrapper so useAutoAway (which depends on WebSocketProvider via
// useSelfPresence → useWebSocket) can be called inside the provider tree
// without ChatLayout itself needing to be a child of WebSocketProvider.
function AutoAwayEffect() {
  useAutoAway();
  return null;
}

// Auth guard (Phase 10) + the two-pane chat shell (Phase 11): a fixed
// sidebar (rooms list + user/theme/logout footer) and a main pane that
// app/chat/page.tsx (empty state) and app/chat/[roomId]/page.tsx (room
// shell) fill in. Below the md breakpoint the sidebar collapses into a
// Sheet drawer (Phase 15) instead of a static column, opened via
// components/MobileSidebarTrigger.tsx (rendered from RoomHeader and the
// empty state, the two screens with no static column to fall back on).
export default function ChatLayout({ children }: { children: React.ReactNode }) {
  const { isReady, isHydrated } = useRequireAuth();
  const sidebarOpen = useUIStore((s) => s.sidebarOpen);
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen);
  const pathname = usePathname();

  // Close the drawer on every navigation (picking a room, or "New room"
  // routing into the room it just created) instead of leaving it open over
  // whatever page comes next.
  useEffect(() => {
    setSidebarOpen(false);
  }, [pathname, setSidebarOpen]);

  if (!isHydrated || !isReady) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background p-8">
        <Skeleton className="h-8 w-48" />
      </div>
    );
  }

  // One WebSocket for the whole authenticated session. The provider lives here
  // (below the auth gate) because this nested layout persists across /chat/*
  // route changes, so the socket survives room switches — exactly one session.
  // h-dvh (not h-screen) tracks the *visual* viewport on mobile: opening the
  // on-screen keyboard shrinks that, not the layout viewport h-screen is based
  // on, so h-dvh is what keeps the composer above the keyboard instead of it
  // being pushed off-screen.
  return (
    <WebSocketProvider>
      <AutoAwayEffect />
      <UnreadTitle />
      <div className="flex h-dvh flex-col bg-background text-foreground">
        <ReconnectingBanner />
        <div className="flex min-h-0 flex-1">
          <aside className="hidden h-full w-64 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex">
            <ChatSidebarContent />
          </aside>

          <Sheet open={sidebarOpen} onOpenChange={setSidebarOpen}>
            <SheetContent
              side="left"
              className="flex w-72 max-w-[85vw] flex-col gap-0 border-sidebar bg-sidebar p-0 text-sidebar-foreground"
              // Closes on any room-link tap, including one back to the room
              // already open — the pathname-keyed effect above only fires on
              // an actual route change, which re-tapping the active room
              // never produces, so the drawer would otherwise stay open over
              // it.
              onClick={(e) => {
                if ((e.target as HTMLElement).closest("a")) setSidebarOpen(false);
              }}
            >
              <SheetHeader className="border-b px-4 py-4 text-left">
                <SheetTitle>ConvoyChat</SheetTitle>
              </SheetHeader>
              <ChatSidebarContent />
            </SheetContent>
          </Sheet>

          <main className="min-w-0 flex-1 overflow-hidden">{children}</main>
        </div>
      </div>
    </WebSocketProvider>
  );
}
