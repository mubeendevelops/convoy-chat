import { Check } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { UserPresence } from "@/components/UserPresence";
import type { ChatMessage } from "@/hooks/useMessages";
import { formatMessageTimestamp } from "@/lib/messages";
import { cn } from "@/lib/utils";

// Locked design decision: every message gets its own full header (avatar +
// username + timestamp), no author/time-window grouping — see plan.md
// Phase 12.
export function MessageBubble({ message, isOwn }: { message: ChatMessage; isOwn: boolean }) {
  const isDeleted = !!message.deleted_at;
  const isSending = message.status === "sending";
  const isFailed = message.status === "failed";
  // Locked (asked, Phase 14): per-message checkmark once at least one other
  // member has read it — read_by is only meaningful once the message is a
  // real, confirmed row (an optimistic/failed bubble's read_by is always []).
  const isRead = isOwn && !message.status && message.read_by.length > 0;

  return (
    <div className={cn("flex items-start gap-3", isOwn && "flex-row-reverse")} data-message-id={message.id}>
      <div className="relative shrink-0">
        <Avatar className="h-9 w-9">
          {message.user.avatar_url && <AvatarImage src={message.user.avatar_url} alt={message.user.username} />}
          <AvatarFallback>{message.user.username.slice(0, 1).toUpperCase()}</AvatarFallback>
        </Avatar>
        <UserPresence userId={message.user.id} className="absolute -bottom-0.5 -right-0.5" />
      </div>

      <div className={cn("flex min-w-0 max-w-[70%] flex-col gap-1", isOwn && "items-end")}>
        <div className={cn("flex items-baseline gap-2 px-1", isOwn && "flex-row-reverse")}>
          <span className="truncate text-sm font-medium">{message.user.username}</span>
          <span
            className="shrink-0 text-xs text-muted-foreground"
            title={new Date(message.created_at).toLocaleString()}
          >
            {formatMessageTimestamp(message.created_at)}
          </span>
        </div>

        <div
          className={cn(
            "whitespace-pre-wrap break-words rounded-2xl px-3 py-2 text-sm",
            isDeleted
              ? "bg-muted italic text-muted-foreground"
              : isOwn
                ? "bg-bubble-outgoing text-bubble-outgoing-foreground"
                : "bg-bubble-incoming text-bubble-incoming-foreground",
            isSending && "opacity-60",
          )}
        >
          {isDeleted ? "This message was deleted" : message.content}
        </div>

        {isFailed && <p className="px-1 text-xs text-destructive">Failed to send</p>}
        {isRead && (
          <span
            className="flex items-center gap-0.5 px-1 text-xs text-muted-foreground"
            title={`Read by ${message.read_by.length} ${message.read_by.length === 1 ? "person" : "people"}`}
          >
            <Check className="h-3 w-3" />
            Read
          </span>
        )}
      </div>
    </div>
  );
}
