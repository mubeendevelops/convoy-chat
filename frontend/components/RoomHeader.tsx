"use client";

import { Users } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { MembersList } from "@/components/MembersList";
import { getRoomDisplayName, roomTypeLabel } from "@/lib/rooms";
import type { RoomDetail } from "@/lib/types";

export function RoomHeader({ room, currentUserId }: { room: RoomDetail; currentUserId: string }) {
  const displayName = getRoomDisplayName(room, currentUserId, room.members);

  return (
    <header className="flex items-center justify-between gap-4 border-b px-6 py-4">
      <div className="flex min-w-0 items-center gap-3">
        <h1 className="truncate text-lg font-semibold">{displayName}</h1>
        <Badge variant="secondary">{roomTypeLabel(room.type)}</Badge>
      </div>

      <Sheet>
        <SheetTrigger asChild>
          <Button variant="ghost" size="sm" className="shrink-0 gap-2">
            <Users className="h-4 w-4" />
            {room.members.length} {room.members.length === 1 ? "member" : "members"}
          </Button>
        </SheetTrigger>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>Members</SheetTitle>
          </SheetHeader>
          <div className="mt-4">
            <MembersList members={room.members} />
          </div>
        </SheetContent>
      </Sheet>
    </header>
  );
}
