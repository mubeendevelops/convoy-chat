import { usePresence } from "@/hooks/usePresence";
import { cn } from "@/lib/utils";
import type { PresenceStatus } from "@/lib/types";

// online reuses the brand cyan token (reserved for online indicators per
// CLAUDE.md's locked palette); away/offline are plain neutral Tailwind
// colors since only online gets the brand reservation.
const STATUS_COLOR: Record<PresenceStatus, string> = {
  online: "bg-primary",
  away: "bg-amber-500",
  offline: "bg-muted-foreground/40",
};

const STATUS_LABEL: Record<PresenceStatus, string> = {
  online: "Online",
  away: "Away",
  offline: "Offline",
};

// A small ring-bordered dot meant to overlay the bottom-right corner of an
// avatar (caller positions it, e.g. wraps the avatar in a `relative` div and
// passes `className="absolute -bottom-0.5 -right-0.5"`) — used on message
// headers (MessageBubble) and the members list (MembersList).
export function UserPresence({ userId, className }: { userId: string; className?: string }) {
  const { status } = usePresence(userId);

  return (
    <span
      role="img"
      aria-label={STATUS_LABEL[status]}
      title={STATUS_LABEL[status]}
      className={cn(
        "block h-2.5 w-2.5 shrink-0 rounded-full ring-2 ring-background",
        STATUS_COLOR[status],
        className,
      )}
    />
  );
}
