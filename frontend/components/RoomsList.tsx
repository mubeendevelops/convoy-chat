"use client";

import { memo } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";

import { BrowseChannelsDialog } from "@/components/BrowseChannelsDialog";
import { CreateRoomDialog } from "@/components/CreateRoomDialog";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuth } from "@/hooks/useAuth";
import { useRoom, useRooms } from "@/hooks/useRooms";
import { getRoomDisplayName } from "@/lib/rooms";
import { cn } from "@/lib/utils";
import type { Room } from "@/lib/types";

const RoomRow = memo(function RoomRow({
  room,
  isActive,
  currentUserId,
}: {
  room: Room;
  isActive: boolean;
  currentUserId: string;
}) {
  // Bare Room (from the list endpoint) has no members embedded — only fetch
  // the detail for direct rooms, since that's the only case where the
  // display name needs it (channels already carry their own `name`).
  const detail = useRoom(room.type === "direct" ? room.id : undefined);
  const displayName = getRoomDisplayName(room, currentUserId, detail.data?.members);

  return (
    <Link
      href={`/chat/${room.id}`}
      aria-current={isActive ? "page" : undefined}
      className={cn(
        "block truncate rounded-md px-3 py-2 text-sm transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-sidebar",
        isActive
          ? "bg-primary text-primary-foreground"
          : "text-sidebar-foreground hover:bg-accent hover:text-accent-foreground",
      )}
    >
      {displayName}
    </Link>
  );
});

export function RoomsList() {
  const { user } = useAuth();
  const params = useParams<{ roomId?: string }>();
  const { data: rooms, isLoading, isError } = useRooms();

  const channels = rooms?.filter((r) => r.type === "channel") ?? [];
  const groups = rooms?.filter((r) => r.type === "group") ?? [];
  const directs = rooms?.filter((r) => r.type === "direct") ?? [];

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="space-y-2">
        <CreateRoomDialog />
        <BrowseChannelsDialog />
      </div>

      <ScrollArea className="flex-1">
        <div className="space-y-4 pr-2">
          {isLoading && (
            <div className="space-y-2">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}

          {isError && <p className="px-3 text-sm text-destructive">Couldn&apos;t load your rooms.</p>}

          {rooms && rooms.length === 0 && (
            <p className="px-3 text-sm text-muted-foreground">
              No rooms yet — create one to get started.
            </p>
          )}

          {channels.length > 0 && (
            <div className="space-y-1">
              <h2 className="px-3 text-xs font-semibold uppercase text-muted-foreground">Channels</h2>
              {channels.map((room) => (
                <RoomRow
                  key={room.id}
                  room={room}
                  isActive={params.roomId === room.id}
                  currentUserId={user?.id ?? ""}
                />
              ))}
            </div>
          )}

          {groups.length > 0 && (
            <div className="space-y-1">
              <h2 className="px-3 text-xs font-semibold uppercase text-muted-foreground">Groups</h2>
              {groups.map((room) => (
                <RoomRow
                  key={room.id}
                  room={room}
                  isActive={params.roomId === room.id}
                  currentUserId={user?.id ?? ""}
                />
              ))}
            </div>
          )}

          {directs.length > 0 && (
            <div className="space-y-1">
              <h2 className="px-3 text-xs font-semibold uppercase text-muted-foreground">
                Direct Messages
              </h2>
              {directs.map((room) => (
                <RoomRow
                  key={room.id}
                  room={room}
                  isActive={params.roomId === room.id}
                  currentUserId={user?.id ?? ""}
                />
              ))}
            </div>
          )}
        </div>
      </ScrollArea>
    </div>
  );
}
