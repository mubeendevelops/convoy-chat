import { memo, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { Check, Pencil, SmilePlus, Trash2, X } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
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
import { Textarea } from "@/components/ui/textarea";
import { UserPresence } from "@/components/UserPresence";
import type { ChatMessage } from "@/hooks/useMessages";
import { formatMessageTimestamp } from "@/lib/messages";
import { cn } from "@/lib/utils";
import { validateMessageContent } from "@/lib/validation";

// Fixed quick-reaction set — deliberately not a full emoji picker (no such
// component exists in this project's shadcn set, see CLAUDE.md; adding one
// wasn't warranted for a "basic" pass). Reacting is REST-only regardless of
// how the emoji is chosen (see CLAUDE.md's WebSocket event contract).
const QUICK_REACTIONS = ["👍", "❤️", "😂", "🎉", "😮", "😢"];

interface MessageBubbleProps {
  message: ChatMessage;
  isOwn: boolean;
  currentUserId: string;
  /** Re-attempts a failed send in place. Omitted messages (e.g. never own,
   * never failed) never render a control that would need it. */
  onRetry?: (clientId: string, content: string) => void;
  /** Toggles the caller's reaction with the given emoji on this message. */
  onToggleReaction?: (messageId: string, emoji: string) => void;
  /** True when the current user is an admin of this room — combined with
   * isOwn to decide whether the delete affordance shows, mirroring the
   * backend's author-or-admin delete rule. */
  isRoomAdmin?: boolean;
  /** Soft-deletes this message. Only wired up when the current user is
   * allowed to (own message or room admin). */
  onDelete?: (messageId: string) => void;
  /** Edits this message's content in place. Unlike onDelete, this is
   * author-only with no admin override (see CLAUDE.md) — isOwn alone gates
   * the affordance, isRoomAdmin never does. */
  onEdit?: (messageId: string, content: string) => void;
  /** True only for the single most-recent own message that's been read.
   * The "Read" indicator shows just on that one message (implying every
   * earlier own message was read too, Slack/WhatsApp style) rather than
   * repeating under every bubble — MessageList decides which one it is. */
  showReadReceipt?: boolean;
}

// Locked design decision: every message gets its own full header (avatar +
// username + timestamp), no author/time-window grouping — see plan.md
// Phase 12. Memoized (Phase 15): a room's list re-renders on every inbound
// WS event (new message, presence flip, typing, read receipt), and most of
// those touch at most one row — memo means the other N-1 bubbles skip re-
// rendering instead of re-computing timestamps/avatars for no reason.
function MessageBubbleComponent({
  message,
  isOwn,
  currentUserId,
  onRetry,
  onToggleReaction,
  isRoomAdmin,
  onDelete,
  onEdit,
  showReadReceipt,
}: MessageBubbleProps) {
  const [pickerOpen, setPickerOpen] = useState(false);
  // Touch has no hover, so the desktop group-hover reveal can't apply — a
  // finger can't rest over an element, and browsers that fake hover-on-
  // first-tap just turn it into a confusing double-tap. Tapping the bubble
  // itself (see the message content div's onClick below) is the mobile
  // equivalent trigger, toggling the same toolbar that hover shows on
  // desktop, mirroring pickerOpen's existing show/hide-on-tap pattern.
  // (Unconditionally showing the toolbar in flow, the previous behavior,
  // both defeated the "hidden until wanted" point of it and, being a
  // block-level flex container once static, stretched to the bubble's full
  // width instead of hugging its own content.)
  const [mobileActionsOpen, setMobileActionsOpen] = useState(false);
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [isEditing, setIsEditing] = useState(false);
  const [editContent, setEditContent] = useState("");
  const editTextareaRef = useRef<HTMLTextAreaElement>(null);
  const isDeleted = !!message.deleted_at;
  const isSending = message.status === "sending";
  const isFailed = message.status === "failed";
  // A "Read" checkmark once at least one other member has read it — read_by is
  // only meaningful on a real, confirmed row (an optimistic/failed bubble's
  // read_by is always []). Only the most-recent read own message actually
  // shows it (showReadReceipt, decided by MessageList), so the indicator isn't
  // repeated under every bubble — see MessageBubbleProps.showReadReceipt.
  const isRead = isOwn && !message.status && message.read_by.length > 0 && !!showReadReceipt;
  // A deleted message 404s on react (mirrors the backend's already-deleted
  // idiom, see CLAUDE.md); a still-optimistic/failed bubble's id is a client
  // nonce, not a real message id yet, so reacting to one would 404 too.
  const canReact = !isDeleted && !message.status;
  // Author-or-admin, matching the backend's DELETE /messages/{id} rule. A
  // deleted or still-optimistic bubble can't be deleted (already gone, or has
  // no real id yet — either would 404). canDelete ⊆ canReact, so its control
  // lives in the same actions row.
  const canDelete = canReact && !!onDelete && (isOwn || !!isRoomAdmin);
  // Author-only, no admin override — matches the backend's PATCH
  // /messages/{id} rule (see CLAUDE.md): a room admin can remove disruptive
  // content but not rewrite someone else's words.
  const canEdit = canReact && !!onEdit && isOwn;
  const editInvalid = validateMessageContent(editContent.trim());

  // Autofocus + place the caret at the end when entering edit mode, rather
  // than selecting all — editing existing text usually means appending or
  // fixing a typo near the end, not retyping from scratch.
  useLayoutEffect(() => {
    if (!isEditing) return;
    const el = editTextareaRef.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, [isEditing]);

  function toggle(emoji: string) {
    onToggleReaction?.(message.id, emoji);
    setPickerOpen(false);
    setMobileActionsOpen(false);
  }

  function confirmDelete() {
    onDelete?.(message.id);
    setConfirmDeleteOpen(false);
  }

  function startEdit() {
    setEditContent(message.content ?? "");
    setIsEditing(true);
    setMobileActionsOpen(false);
  }

  function cancelEdit() {
    setIsEditing(false);
  }

  function saveEdit() {
    const trimmed = editContent.trim();
    if (validateMessageContent(trimmed)) return;
    // No-op if unchanged — nothing to send, and it saves the recipient side
    // an idempotent-but-pointless broadcast.
    if (trimmed !== message.content) {
      onEdit?.(message.id, trimmed);
    }
    setIsEditing(false);
  }

  function handleEditKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      saveEdit();
    } else if (e.key === "Escape") {
      e.preventDefault();
      cancelEdit();
    }
  }

  return (
    <div className={cn("group flex items-start gap-3", isOwn && "flex-row-reverse")} data-message-id={message.id}>
      <div className="relative shrink-0">
        <Avatar className="h-9 w-9">
          {message.user.avatar_url && <AvatarImage src={message.user.avatar_url} alt={message.user.username} />}
          <AvatarFallback>{message.user.username.slice(0, 1).toUpperCase()}</AvatarFallback>
        </Avatar>
        <UserPresence userId={message.user.id} className="absolute -bottom-0.5 -right-0.5" />
      </div>

      <div className={cn("flex min-w-0 max-w-[70%] flex-col gap-1", isOwn ? "items-end" : "items-start")}>
        <div className={cn("flex items-baseline gap-2 px-1", isOwn && "flex-row-reverse")}>
          <span className="truncate text-sm font-medium">{message.user.username}</span>
          <span
            className="shrink-0 text-xs text-muted-foreground"
            title={new Date(message.created_at).toLocaleString()}
          >
            {formatMessageTimestamp(message.created_at)}
          </span>
          {!isDeleted && message.edited_at && (
            <span
              className="shrink-0 text-xs text-muted-foreground"
              title={`Edited ${new Date(message.edited_at).toLocaleString()}`}
            >
              (edited)
            </span>
          )}
        </div>

        {isEditing ? (
          <div className="flex w-full flex-col gap-1.5">
            <Textarea
              ref={editTextareaRef}
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              onKeyDown={handleEditKeyDown}
              aria-label="Edit message"
              rows={1}
              className="min-h-[44px] w-full resize-none overflow-y-auto text-sm"
            />
            <div className={cn("flex items-center gap-2", isOwn && "flex-row-reverse")}>
              <Button size="sm" onClick={saveEdit} disabled={!!editInvalid}>
                <Check className="mr-1 h-3.5 w-3.5" />
                Save
              </Button>
              <Button size="sm" variant="ghost" onClick={cancelEdit}>
                <X className="mr-1 h-3.5 w-3.5" />
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <div className="relative">
            <div
              onClick={canReact ? () => setMobileActionsOpen((v) => !v) : undefined}
              className={cn(
                "w-fit max-w-full whitespace-pre-wrap break-words rounded-2xl px-3 py-2 text-sm",
                // Without an explicit width, a block box like this one
                // normally fills 100% of its container rather than sizing to
                // its own content — invisible as long as this bubble is the
                // widest thing in `relative`'s box, but once the mobile
                // toolbar below opens and is wider than a short message
                // (e.g. "hi"), `relative`'s shrink-to-fit width grows to
                // match the toolbar, and without w-fit this bubble would
                // stretch to fill that now-wider box instead of staying
                // sized to its own text — the reported padding-spans-the-
                // toolbar bug. ml-auto re-pins an own message to the right
                // edge of that wider box (mirrors the toolbar's own
                // max-md:ml-auto below) — otherwise a block box narrower
                // than its container sits at the left (LTR) by default,
                // which would misalign a right-side/own bubble.
                isOwn && "ml-auto",
                isDeleted
                  ? "bg-muted italic text-muted-foreground"
                  : isOwn
                    ? "bg-bubble-outgoing text-bubble-outgoing-foreground"
                    : "bg-bubble-incoming text-bubble-incoming-foreground",
                isSending && "opacity-60",
                canReact && "max-md:cursor-pointer",
              )}
            >
              {isDeleted ? "This message was deleted" : message.content}
            </div>

            {canReact && (
              // Floating action toolbar. On desktop it stays hidden until the
              // message is hovered/focused and sits in the gutter beside the
              // bubble, so it reserves no vertical space and consecutive
              // messages stack tightly. Touch has no hover to mirror — a
              // finger can't rest over an element the way a cursor can, and
              // browsers that fake hover-on-first-tap just turn it into a
              // confusing double-tap — so on touch (max-md) tapping the
              // bubble itself (see onClick above) is the equivalent trigger:
              // it toggles the same toolbar into normal flow below the
              // bubble (there's rarely gutter room for the whole toolbar on
              // a narrow phone), hugging its own content width
              // (max-md:w-fit) rather than stretching block-level to the
              // bubble's full width, and hidden until tapped rather than
              // always occupying space.
              <div
                className={cn(
                  "z-10 flex items-center gap-0.5 rounded-full border bg-popover px-1 py-0.5 shadow-sm transition-opacity",
                  "absolute top-1/2 -translate-y-1/2",
                  isOwn ? "right-full mr-1" : "left-full ml-1",
                  "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100",
                  pickerOpen && "pointer-events-auto opacity-100",
                  mobileActionsOpen
                    ? cn(
                        "max-md:pointer-events-auto max-md:static max-md:mt-1 max-md:w-fit max-md:translate-y-0 max-md:opacity-100",
                        isOwn && "max-md:ml-auto",
                      )
                    : "max-md:hidden",
                )}
              >
                {pickerOpen &&
                  QUICK_REACTIONS.map((emoji) => (
                    <button
                      key={emoji}
                      type="button"
                      onClick={() => toggle(emoji)}
                      aria-label={`React with ${emoji}`}
                      className="rounded-full p-1 text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    >
                      {emoji}
                    </button>
                  ))}
                <button
                  type="button"
                  onClick={() => setPickerOpen((v) => !v)}
                  aria-pressed={pickerOpen}
                  aria-label="Add reaction"
                  className="rounded-full p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                >
                  <SmilePlus className="h-3.5 w-3.5" />
                </button>
                {canEdit && (
                  <button
                    type="button"
                    onClick={startEdit}
                    aria-label="Edit message"
                    className="rounded-full p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </button>
                )}
                {canDelete && (
                  <Dialog open={confirmDeleteOpen} onOpenChange={setConfirmDeleteOpen}>
                    <DialogTrigger asChild>
                      <button
                        type="button"
                        aria-label="Delete message"
                        className="rounded-full p-1 text-muted-foreground hover:bg-muted hover:text-destructive focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </DialogTrigger>
                    <DialogContent>
                      <DialogHeader>
                        <DialogTitle>Delete message?</DialogTitle>
                        <DialogDescription>
                          This can&apos;t be undone. The message will be replaced with a &ldquo;message
                          deleted&rdquo; placeholder.
                        </DialogDescription>
                      </DialogHeader>
                      <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setConfirmDeleteOpen(false)}>
                          Cancel
                        </Button>
                        <Button type="button" variant="destructive" onClick={confirmDelete}>
                          Delete
                        </Button>
                      </DialogFooter>
                    </DialogContent>
                  </Dialog>
                )}
              </div>
            )}
          </div>
        )}

        {canReact && !isEditing && message.reactions.length > 0 && (
          <div className={cn("flex flex-wrap items-center gap-1 px-1", isOwn && "flex-row-reverse")}>
            {message.reactions.map((r) => {
              const mine = r.user_ids.includes(currentUserId);
              return (
                <button
                  key={r.emoji}
                  type="button"
                  onClick={() => toggle(r.emoji)}
                  aria-pressed={mine}
                  aria-label={`${mine ? "Remove" : "Add"} ${r.emoji} reaction, ${r.count} ${r.count === 1 ? "person" : "people"}`}
                  className={cn(
                    "flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                    mine ? "border-primary bg-primary/10" : "border-border bg-background hover:bg-muted",
                  )}
                >
                  <span>{r.emoji}</span>
                  <span className="text-muted-foreground">{r.count}</span>
                </button>
              );
            })}
          </div>
        )}

        {isFailed && (
          <p className="flex items-center gap-1.5 px-1 text-xs text-destructive">
            Failed to send
            <button
              type="button"
              onClick={() => onRetry?.(message.id, message.content ?? "")}
              className="rounded underline underline-offset-2 hover:no-underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              Retry
            </button>
          </p>
        )}
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

export const MessageBubble = memo(MessageBubbleComponent);
