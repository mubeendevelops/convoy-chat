"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { MessageInput } from "@/components/MessageInput";
import { MessageList } from "@/components/MessageList";
import { RoomHeader } from "@/components/RoomHeader";
import { TypingIndicator } from "@/components/TypingIndicator";
import { useDeleteMessage, useEditMessage, useMessages, useSendMessage, useToggleReaction } from "@/hooks/useMessages";
import { useRoomPresence } from "@/hooks/usePresence";
import { useMarkRoomRead } from "@/hooks/useRooms";
import { useTyping } from "@/hooks/useTyping";
import { useWebSocket } from "@/hooks/useWebSocket";
import type { RoomDetail } from "@/lib/types";

// Composes the room shell: header + scrollable history + composer. Callers
// should mount this with `key={room.id}` (see app/chat/[roomId]/page.tsx) so
// switching rooms remounts it fresh rather than carrying over the previous
// room's scroll position and in-flight-send state.
export function ChatWindow({ room, currentUserId }: { room: RoomDetail; currentUserId: string }) {
  const router = useRouter();
  const { messages, isLoading, isError, hasNextPage, isFetchingNextPage, fetchNextPage } = useMessages(room.id);
  const { send: sendMessage, retry: retryMessage } = useSendMessage(room.id);
  const toggleReaction = useToggleReaction();
  const deleteMessage = useDeleteMessage(room.id);
  const editMessage = useEditMessage(room.id);
  const { setActiveRoom, subscribe } = useWebSocket();
  const markRoomRead = useMarkRoomRead();
  const markRead = markRoomRead.mutate;
  const { typingUserIds, notifyTyping, stopTyping } = useTyping(room.id);

  // Seed the presence store with this room's members' current statuses on open,
  // so a peer who went online before this session's socket connected shows
  // online immediately rather than defaulting to "offline" until the next live
  // event (see useRoomPresence / usePresence).
  useRoomPresence(room.id);

  // The backend lets a message's author *or* a room admin delete it; the
  // affordance mirrors that. Author is per-message (isOwn in the list); admin
  // is this room-level flag. A DM has no admin, so there only the author can.
  const isRoomAdmin = room.members.some((m) => m.user.id === currentUserId && m.role === "admin");

  // The provider now keeps this socket subscribed to *every* room the whole
  // session (so unread badges update live), so ChatWindow no longer joins/
  // leaves the live stream itself. Instead it reports which room is on screen
  // — so message.new skips bumping this room's unread badge — and marks the
  // room read on open and on close. Marking on close covers messages that
  // arrived while it was the active room (excluded from the live bump), so a
  // later reload doesn't resurface them. Keyed by room.id, ChatWindow remounts
  // per room, so this is one clean open/close pair.
  useEffect(() => {
    setActiveRoom(room.id);
    markRead(room.id);
    return () => {
      setActiveRoom(null);
      markRead(room.id);
    };
  }, [room.id, setActiveRoom, markRead]);

  // "Routed out live" for a kick (or a self-leave from another tab): the
  // central routeEvent handler in useWebSocket.tsx already does the
  // data-layer cleanup (cache invalidation, Hub-side WS unsubscribe) for any
  // user.left about ourselves — it has no router, though, so it can't
  // navigate. This component-level subscribe() listener is exactly for that:
  // it's scoped to *this* room (the one currently being viewed), so it only
  // fires the navigation when the departure is actually relevant to what's
  // on screen right now, not for every room the user happens to belong to.
  useEffect(() => {
    return subscribe("user.left", (event) => {
      if (event.room_id === room.id && event.user_id === currentUserId) {
        router.replace("/chat");
      }
    });
  }, [room.id, currentUserId, subscribe, router]);

  function handleSend(content: string) {
    // Sending implies typing has stopped — no reason to wait out the idle
    // debounce once the message is already on its way.
    stopTyping();
    sendMessage(content);
  }

  function handleEdit(messageId: string, content: string) {
    editMessage.mutate({ messageId, content });
  }

  return (
    <div className="flex h-full flex-col">
      <RoomHeader room={room} currentUserId={currentUserId} />
      <MessageList
        messages={messages}
        currentUserId={currentUserId}
        isLoading={isLoading}
        isError={isError}
        hasNextPage={!!hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        onLoadOlder={() => void fetchNextPage()}
        onRetry={retryMessage}
        onToggleReaction={toggleReaction}
        isRoomAdmin={isRoomAdmin}
        onDelete={deleteMessage.mutate}
        onEdit={handleEdit}
      />
      <TypingIndicator typingUserIds={typingUserIds} members={room.members} />
      <MessageInput onSend={handleSend} onTyping={(value) => (value.trim() ? notifyTyping() : stopTyping())} />
    </div>
  );
}
