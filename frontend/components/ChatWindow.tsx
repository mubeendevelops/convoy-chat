"use client";

import { MessageInput } from "@/components/MessageInput";
import { MessageList } from "@/components/MessageList";
import { RoomHeader } from "@/components/RoomHeader";
import { useMessages, useSendMessage } from "@/hooks/useMessages";
import type { RoomDetail } from "@/lib/types";

// Composes the room shell: header + scrollable history + composer. Callers
// should mount this with `key={room.id}` (see app/chat/[roomId]/page.tsx) so
// switching rooms remounts it fresh rather than carrying over the previous
// room's scroll position and in-flight-send state.
export function ChatWindow({ room, currentUserId }: { room: RoomDetail; currentUserId: string }) {
  const { messages, isLoading, isError, hasNextPage, isFetchingNextPage, fetchNextPage } = useMessages(room.id);
  const sendMessage = useSendMessage(room.id);

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
      <MessageInput sendMessage={sendMessage} />
    </div>
  );
}
