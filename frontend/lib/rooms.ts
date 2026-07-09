import type { Room, RoomMemberWithUser, RoomType } from "./types";

// Channels carry their own name; direct rooms don't (backend leaves `name`
// null for them — see CLAUDE.md), so their "name" is the other participant.
// `members` is only available once a RoomDetail has loaded (the bare Room
// shape from the list endpoint doesn't embed them) — falls back to a
// generic label until then.
export function getRoomDisplayName(
  room: Pick<Room, "type" | "name">,
  currentUserId: string,
  members?: RoomMemberWithUser[],
): string {
  if (room.type === "direct") {
    const peer = members?.find((m) => m.user.id !== currentUserId);
    return peer ? peer.user.username : "Direct Message";
  }
  return room.name ?? "Untitled Channel";
}

export function roomTypeLabel(type: RoomType): string {
  switch (type) {
    case "channel":
      return "Channel";
    case "direct":
      return "Direct Message";
    case "group":
      return "Group";
  }
}
