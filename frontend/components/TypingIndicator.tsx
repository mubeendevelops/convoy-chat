import type { RoomMemberWithUser } from "@/lib/types";

function usernameFor(userId: string, members: RoomMemberWithUser[]): string {
  return members.find((m) => m.user.id === userId)?.user.username ?? "Someone";
}

// Renders inbound typing state as one of exactly two shapes (locked scope,
// plan.md Phase 14): a single typer's name, or a generic "Several people"
// once more than one other member is typing at once — no attempt at listing
// multiple names. Always occupies its row (even when empty) so the message
// list doesn't shift as typing starts/stops.
export function TypingIndicator({
  typingUserIds,
  members,
}: {
  typingUserIds: string[];
  members: RoomMemberWithUser[];
}) {
  const text =
    typingUserIds.length === 0
      ? null
      : typingUserIds.length === 1
        ? `${usernameFor(typingUserIds[0], members)} is typing…`
        : "Several people are typing…";

  return <div className="h-5 truncate px-6 text-xs italic text-muted-foreground">{text}</div>;
}
