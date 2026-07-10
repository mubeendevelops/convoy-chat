"use client";

import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { useRoom } from "@/hooks/useRooms";

// Read-only member view for the admin dashboard's "View members" action —
// deliberately NOT MembersList, which is mutation-heavy (promote/demote/
// kick) and assumes the viewer is themselves a member of the room. A system
// admin viewing a room they don't belong to shouldn't see controls that
// would just 403 (see plan.md's admin-dashboard proposal — system-admin
// power doesn't extend to membership management in rooms they aren't in).
// Reuses GET /rooms/{room_id} via the existing useRoom hook, which the
// backend now lets a system admin call for any room, not just their own.
export function AdminRoomMembersSheet({
  roomId,
  open,
  onOpenChange,
}: {
  roomId: string | undefined;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { data: room, isLoading, isError } = useRoom(roomId);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{room?.name ?? "Members"}</SheetTitle>
        </SheetHeader>
        <div className="mt-4 space-y-3">
          {isLoading && (
            <>
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </>
          )}
          {isError && <p className="text-sm text-destructive">Couldn&apos;t load members.</p>}
          {room?.members.map((member) => (
            <div key={member.user.id} className="flex items-center gap-3">
              <Avatar className="h-8 w-8">
                <AvatarFallback>{member.user.username.slice(0, 1).toUpperCase()}</AvatarFallback>
              </Avatar>
              <span className="min-w-0 flex-1 truncate text-sm">{member.user.username}</span>
              <Badge variant="outline">{member.role}</Badge>
            </div>
          ))}
        </div>
      </SheetContent>
    </Sheet>
  );
}
