"use client";

import { useEffect } from "react";

import { MessageInput } from "@/components/MessageInput";
import { MessageList } from "@/components/MessageList";
import { RoomHeader } from "@/components/RoomHeader";
import { useMessages, useSendMessage } from "@/hooks/useMessages";
import { useWebSocket } from "@/hooks/useWebSocket";
import type { RoomDetail } from "@/lib/types";

// Composes the room shell: header + scrollable history + composer. Callers
// should mount this with `key={room.id}` (see app/chat/[roomId]/page.tsx) so
// switching rooms remounts it fresh rather than carrying over the previous
// room's scroll position and in-flight-send state.
export function ChatWindow({ room, currentUserId }: { room: RoomDetail; currentUserId: string }) {
  const { messages, isLoading, isError, hasNextPage, isFetchingNextPage, fetchNextPage } = useMessages(room.id);
  const sendMessage = useSendMessage(room.id);
  const { joinRoom, leaveRoom } = useWebSocket();

  // Join this room's live stream while it's open; leave on switch/close. Since
  // ChatWindow is keyed by room.id it remounts per room, so this is one clean
  // join-on-open / leave-on-unmount. If the socket isn't open yet, the join is
  // remembered and (re)sent by the provider once it connects.
  useEffect(() => {
    joinRoom(room.id);
    return () => leaveRoom(room.id);
  }, [room.id, joinRoom, leaveRoom]);

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
      />
      <MessageInput onSend={sendMessage} />
    </div>
  );
}
