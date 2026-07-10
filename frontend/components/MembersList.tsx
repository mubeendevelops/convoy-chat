"use client";

import { useState } from "react";
import { ShieldCheck, ShieldOff, UserMinus } from "lucide-react";

import { Avatar, AvatarFallback } from "@/components/ui/avatar";
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
import { UserPresence } from "@/components/UserPresence";
import { useChangeMemberRole, useRemoveMember } from "@/hooks/useRooms";
import type { RoomMemberWithUser } from "@/lib/types";

interface MembersListProps {
  members: RoomMemberWithUser[];
  roomId: string;
  currentUserId: string;
  /** Whether the *viewer* is an admin of this room — gates the promote/
   * demote/kick controls. A DM has no admin, so this is always false there
   * and the list renders as plain read-only rows, same as before this. */
  isViewerAdmin: boolean;
}

// Room members with live presence dots, rendered inside RoomHeader's members
// Sheet. Kept in the API's own order (no online-first re-sorting) so rows
// don't jump around as presence flickers. Promote/demote/kick controls are
// admin-only (server-enforced; isViewerAdmin just decides whether to render
// them at all) — no optimistic update on either action, since the live
// member.role_changed/user.left broadcast (routed centrally in
// useWebSocket's routeEvent) is what actually refreshes this list, the same
// "REST call + live broadcast does the work" pattern already used for
// reactions.
export function MembersList({ members, roomId, currentUserId, isViewerAdmin }: MembersListProps) {
  const [kickTarget, setKickTarget] = useState<RoomMemberWithUser | null>(null);
  const changeRole = useChangeMemberRole(roomId);
  const removeMember = useRemoveMember(roomId);

  const adminCount = members.filter((m) => m.role === "admin").length;

  function confirmKick() {
    if (!kickTarget) return;
    removeMember.mutate(kickTarget.user.id);
    setKickTarget(null);
  }

  return (
    <div className="space-y-3">
      {members.map((member) => {
        const isSelf = member.user.id === currentUserId;
        // Non-authoritative UX mirror of the backend's ErrLastAdmin rule
        // (see CLAUDE.md) — the server is still the real gate.
        const isLastAdmin = member.role === "admin" && adminCount <= 1;
        const isChangingThisRole = changeRole.isPending && changeRole.variables?.userId === member.user.id;
        const isKickingThisMember = removeMember.isPending && removeMember.variables === member.user.id;

        return (
          <div key={member.user.id} className="flex items-center gap-3">
            <div className="relative shrink-0">
              <Avatar className="h-8 w-8">
                <AvatarFallback>{member.user.username.slice(0, 1).toUpperCase()}</AvatarFallback>
              </Avatar>
              <UserPresence userId={member.user.id} className="absolute -bottom-0.5 -right-0.5" />
            </div>
            <span className="min-w-0 flex-1 truncate text-sm">{member.user.username}</span>
            <Badge variant="outline">{member.role}</Badge>

            {isViewerAdmin && (
              <div className="flex shrink-0 items-center gap-1">
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  disabled={isChangingThisRole || (member.role === "admin" && isLastAdmin)}
                  title={
                    member.role === "admin"
                      ? isLastAdmin
                        ? "Can't demote the room's only admin"
                        : "Remove admin"
                      : "Make admin"
                  }
                  aria-label={member.role === "admin" ? `Remove admin from ${member.user.username}` : `Make ${member.user.username} an admin`}
                  onClick={() =>
                    changeRole.mutate({
                      userId: member.user.id,
                      role: member.role === "admin" ? "member" : "admin",
                    })
                  }
                >
                  {member.role === "admin" ? (
                    <ShieldOff className="h-3.5 w-3.5" />
                  ) : (
                    <ShieldCheck className="h-3.5 w-3.5" />
                  )}
                </Button>

                {!isSelf && (
                  <Dialog open={kickTarget?.user.id === member.user.id} onOpenChange={(open) => !open && setKickTarget(null)}>
                    <DialogTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-muted-foreground hover:text-destructive"
                        disabled={isKickingThisMember}
                        aria-label={`Remove ${member.user.username} from this room`}
                        onClick={() => setKickTarget(member)}
                      >
                        <UserMinus className="h-3.5 w-3.5" />
                      </Button>
                    </DialogTrigger>
                    <DialogContent>
                      <DialogHeader>
                        <DialogTitle>Remove {member.user.username}?</DialogTitle>
                        <DialogDescription>
                          They&apos;ll be removed from this room immediately and will need a new invite to
                          rejoin.
                        </DialogDescription>
                      </DialogHeader>
                      <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setKickTarget(null)}>
                          Cancel
                        </Button>
                        <Button type="button" variant="destructive" onClick={confirmKick}>
                          Remove
                        </Button>
                      </DialogFooter>
                    </DialogContent>
                  </Dialog>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
