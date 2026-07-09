"use client";

import { LogOut } from "lucide-react";

import { RoomsList } from "@/components/RoomsList";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuth, useRequireAuth } from "@/hooks/useAuth";
import { WebSocketProvider } from "@/hooks/useWebSocket";

// Auth guard (Phase 10) + the two-pane chat shell (Phase 11): a fixed
// sidebar (rooms list + user/theme/logout footer) and a main pane that
// app/chat/page.tsx (empty state) and app/chat/[roomId]/page.tsx (room
// shell) fill in.
export default function ChatLayout({ children }: { children: React.ReactNode }) {
  const { isReady, isHydrated } = useRequireAuth();
  const { user, logout } = useAuth();

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
  return (
    <WebSocketProvider>
      <div className="flex h-screen bg-background text-foreground">
        <aside className="flex h-full w-64 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground">
          <div className="min-h-0 flex-1 p-4">
            <RoomsList />
          </div>
          <div className="flex items-center justify-between gap-2 border-t p-4">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{user?.username}</p>
              <p className="truncate text-xs text-muted-foreground">{user?.email}</p>
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <ThemeToggle />
              <Button variant="ghost" size="icon" aria-label="Log out" onClick={logout}>
                <LogOut className="h-4 w-4" />
              </Button>
            </div>
          </div>
        </aside>
        <main className="min-w-0 flex-1 overflow-hidden">{children}</main>
      </div>
    </WebSocketProvider>
  );
}
