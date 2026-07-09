"use client";

import { useEffect } from "react";

import { MessageInput } from "@/components/MessageInput";
import { MessageList } from "@/components/MessageList";
import { RoomHeader } from "@/components/RoomHeader";
import { TypingIndicator } from "@/components/TypingIndicator";
import { useMessages, useSendMessage, useToggleReaction } from "@/hooks/useMessages";
import { useTyping } from "@/hooks/useTyping";
import { useWebSocket } from "@/hooks/useWebSocket";
import type { RoomDetail } from "@/lib/types";

// Composes the room shell: header + scrollable history + composer. Callers
// should mount this with `key={room.id}` (see app/chat/[roomId]/page.tsx) so
// switching rooms remounts it fresh rather than carrying over the previous
// room's scroll position and in-flight-send state.
export function ChatWindow({ room, currentUserId }: { room: RoomDetail; currentUserId: string }) {
  const { messages, isLoading, isError, hasNextPage, isFetchingNextPage, fetchNextPage } = useMessages(room.id);
  const { send: sendMessage, retry: retryMessage } = useSendMessage(room.id);
  const toggleReaction = useToggleReaction();
  const { joinRoom, leaveRoom } = useWebSocket();
  const { typingUserIds, notifyTyping, stopTyping } = useTyping(room.id);

  // Join this room's live stream while it's open; leave on switch/close. Since
  // ChatWindow is keyed by room.id it remounts per room, so this is one clean
  // join-on-open / leave-on-unmount. If the socket isn't open yet, the join is
  // remembered and (re)sent by the provider once it connects.
  useEffect(() => {
    joinRoom(room.id);
    return () => leaveRoom(room.id);
  }, [room.id, joinRoom, leaveRoom]);

  function handleSend(content: string) {
    // Sending implies typing has stopped — no reason to wait out the idle
    // debounce once the message is already on its way.
    stopTyping();
    sendMessage(content);
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
      />
      <TypingIndicator typingUserIds={typingUserIds} members={room.members} />
      <MessageInput onSend={handleSend} onTyping={(value) => (value.trim() ? notifyTyping() : stopTyping())} />
    </div>
  );
}
