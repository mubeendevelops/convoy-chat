"use client";

import { LogOut } from "lucide-react";

import { RoomsList } from "@/components/RoomsList";
import { SelfPresenceControl } from "@/components/SelfPresenceControl";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/hooks/useAuth";

// The rooms list + user/theme/logout footer, shared by the always-visible
// desktop sidebar and the mobile Sheet drawer (see app/chat/layout.tsx) so
// the two surfaces can't drift apart.
export function ChatSidebarContent() {
  const { user, logout } = useAuth();

  return (
    <>
      <div className="min-h-0 flex-1 p-4">
        <RoomsList />
      </div>
      <div className="flex items-center justify-between gap-2 border-t p-4">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{user?.username}</p>
          <p className="truncate text-xs text-muted-foreground">{user?.email}</p>
          <SelfPresenceControl />
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <ThemeToggle />
          <Button variant="ghost" size="icon" aria-label="Log out" onClick={logout}>
            <LogOut className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </>
  );
}
