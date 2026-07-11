"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { LogOut, Users } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { InviteMemberDialog } from "@/components/InviteMemberDialog";
import { MembersList } from "@/components/MembersList";
import { MobileSidebarTrigger } from "@/components/MobileSidebarTrigger";
import { useLeaveRoom } from "@/hooks/useRooms";
import { getRoomDisplayName, roomTypeLabel } from "@/lib/rooms";
import type { RoomDetail } from "@/lib/types";

export function RoomHeader({ room, currentUserId }: { room: RoomDetail; currentUserId: string }) {
  const router = useRouter();
  const displayName = getRoomDisplayName(room, currentUserId, room.members);
  const leaveRoom = useLeaveRoom();
  const [confirmOpen, setConfirmOpen] = useState(false);

  // A DM has no admin and reads as a conversation, not a room — label the
  // action accordingly, but the backend membership-leave is the same call.
  const isDirect = room.type === "direct";
  const leaveLabel = isDirect ? "Leave conversation" : "Leave room";

  // Type-agnostic: any room type with an admin concept works the same way
  // here — a DM has no admin member at all, so this is naturally false
  // there without a type-specific check, same idiom the backend uses.
  const isRoomAdmin = room.members.some((m) => m.user.id === currentUserId && m.role === "admin");

  // The invite endpoint is admin-only and isn't type-restricted server-side
  // (it works for a group exactly the same as a channel) — DMs are excluded
  // via isRoomAdmin alone, since they have no admin to satisfy the check.
  const canInvite = isRoomAdmin;

  async function handleLeave() {
    try {
      await leaveRoom.mutateAsync(room.id);
      setConfirmOpen(false);
      router.push("/chat");
    } catch {
      // Toast is surfaced by the hook; keep the dialog open so the user can retry.
    }
  }

  return (
    <header className="flex items-center justify-between gap-4 border-b px-3 py-4 md:px-6">
      <div className="flex min-w-0 flex-1 items-center gap-1 md:gap-3">
        <MobileSidebarTrigger />
        <h1 className="min-w-0 truncate text-lg font-semibold">{displayName}</h1>
        <Badge variant="secondary" className="hidden shrink-0 sm:inline-flex">
          {roomTypeLabel(room.type)}
        </Badge>
      </div>

      <div className="flex shrink-0 items-center gap-1">
        {canInvite && <InviteMemberDialog roomId={room.id} roomName={displayName} />}

        <Sheet>
          <SheetTrigger asChild>
            <Button variant="ghost" size="sm" className="gap-2">
              <Users className="h-4 w-4" />
              {room.members.length} {room.members.length === 1 ? "member" : "members"}
            </Button>
          </SheetTrigger>
          <SheetContent>
            <SheetHeader>
              <SheetTitle>Members</SheetTitle>
            </SheetHeader>
            <div className="mt-4">
              <MembersList
                members={room.members}
                roomId={room.id}
                currentUserId={currentUserId}
                isViewerAdmin={isRoomAdmin}
              />
            </div>
          </SheetContent>
        </Sheet>

        <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <DialogTrigger asChild>
            <Button variant="ghost" size="sm" className="gap-2 text-muted-foreground hover:text-destructive">
              <LogOut className="h-4 w-4" />
              <span className="hidden sm:inline">{leaveLabel}</span>
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{leaveLabel}?</DialogTitle>
              <DialogDescription>
                {isDirect
                  ? "You'll be removed from this conversation and it will disappear from your list. You can start it again later."
                  : "You'll be removed from this room and it will disappear from your list. You'll need an invite to rejoin."}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setConfirmOpen(false)}>
                Cancel
              </Button>
              <Button type="button" variant="destructive" onClick={handleLeave} disabled={leaveRoom.isPending}>
                {leaveRoom.isPending ? "Leaving..." : leaveLabel}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
    </header>
  );
}
