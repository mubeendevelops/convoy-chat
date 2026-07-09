import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { UserPresence } from "@/components/UserPresence";
import type { RoomMemberWithUser } from "@/lib/types";

// Room members with live presence dots, rendered inside RoomHeader's members
// Sheet. Kept in the API's own order (no online-first re-sorting) so rows
// don't jump around as presence flickers.
export function MembersList({ members }: { members: RoomMemberWithUser[] }) {
  return (
    <div className="space-y-3">
      {members.map((member) => (
        <div key={member.user.id} className="flex items-center gap-3">
          <div className="relative shrink-0">
            <Avatar className="h-8 w-8">
              <AvatarFallback>{member.user.username.slice(0, 1).toUpperCase()}</AvatarFallback>
            </Avatar>
            <UserPresence userId={member.user.id} className="absolute -bottom-0.5 -right-0.5" />
          </div>
          <span className="min-w-0 flex-1 truncate text-sm">{member.user.username}</span>
          <Badge variant="outline">{member.role}</Badge>
        </div>
      ))}
    </div>
  );
}
