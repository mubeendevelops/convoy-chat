"use client";

import Link from "next/link";

import { ChatWindow } from "@/components/ChatWindow";
import { MobileSidebarTrigger } from "@/components/MobileSidebarTrigger";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuth } from "@/hooks/useAuth";
import { useRoom } from "@/hooks/useRooms";
import { ApiError } from "@/lib/api";

export default function RoomPage({ params }: { params: { roomId: string } }) {
  const { user } = useAuth();
  const { data: room, isLoading, error } = useRoom(params.roomId);

  if (isLoading) {
    return (
      <div className="flex h-full flex-col">
        <div className="flex items-center gap-2 border-b px-3 py-4 md:px-6">
          <MobileSidebarTrigger />
          <Skeleton className="h-7 w-48" />
        </div>
        <div className="flex-1 p-6">
          <Skeleton className="h-full w-full" />
        </div>
      </div>
    );
  }

  if (error || !room) {
    // GetRoom returns 403 for both "doesn't exist" and "not a member" (see
    // CLAUDE.md) — one generic recovery path covers both correctly.
    const message = error instanceof ApiError ? error.message : "Couldn't load this room.";
    return (
      <div className="flex h-full flex-col">
        <div className="flex items-center gap-2 border-b px-3 py-4 md:px-6 md:hidden">
          <MobileSidebarTrigger />
        </div>
        <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
          <p className="text-sm text-muted-foreground">{message}</p>
          <Link href="/chat" className="text-sm text-primary underline-offset-4 hover:underline">
            Back to rooms
          </Link>
        </div>
      </div>
    );
  }

  // Keyed by room id so switching rooms remounts ChatWindow fresh instead
  // of carrying over the previous room's scroll position / send state.
  return <ChatWindow key={room.id} room={room} currentUserId={user?.id ?? ""} />;
}
