"use client";

import { useCallback } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";

import { useWebSocket } from "@/hooks/useWebSocket";
import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import {
  flattenSortedDedup,
  markFailed,
  messagesQueryKey,
  nextCursor,
  PAGE_SIZE,
  upsertMessages,
  type ChatMessage,
  type MessagesData,
  type MessagesPage,
} from "@/lib/messagesCache";
import type { MessageWithAuthor } from "@/lib/types";

// ChatMessage moved to lib/messagesCache (shared with the WS provider); re-
// exported here so existing `@/hooks/useMessages` imports keep working.
export type { ChatMessage } from "@/lib/messagesCache";

// A WS send that never echoes back a message.new within this window is treated
// as failed (socket dropped between our write and the server persisting it).
const WS_ACK_TIMEOUT_MS = 10000;

// Paginated room history, newest at the bottom. Call fetchNextPage() when the
// user scrolls to the top to load the next-older page. Live messages, optimistic
// sends, and reconnect resync all merge into the same cache; flattenSortedDedup
// gives a stable ascending render order regardless of how rows got there.
export function useMessages(roomId: string | undefined) {
  const query = useInfiniteQuery({
    queryKey: messagesQueryKey(roomId),
    queryFn: ({ pageParam }: { pageParam: string | undefined }) =>
      api.get<MessagesPage>(
        `/api/v1/rooms/${roomId}/messages?limit=${PAGE_SIZE}` +
          (pageParam ? `&before=${encodeURIComponent(pageParam)}` : ""),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => nextCursor(lastPage),
    enabled: !!roomId,
  });

  const messages = flattenSortedDedup(query.data);
  return { ...query, messages };
}

// Sends a message, preferring the live WebSocket and falling back to REST when
// the socket is down. Either way an optimistic bubble appears immediately:
//   - WS path: message.send goes out with a client_id nonce; the server echoes
//     it in message.new, and the WS provider reconciles the bubble centrally.
//     An ack timeout flips a never-echoed bubble to "failed".
//   - REST path (socket down): POST with Idempotency-Key, reconciled on success
//     and marked "failed" on error — the unchanged Phase 12 behavior.
// Returns a plain send(content) — no pending state, so sends are rapid-fire.
export function useSendMessage(roomId: string) {
  const queryClient = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);
  const { send } = useWebSocket();

  return useCallback(
    (content: string) => {
      const clientId = crypto.randomUUID();
      const currentUserId = currentUser?.id ?? clientId;
      const queryKey = messagesQueryKey(roomId);
      const now = new Date().toISOString();

      const optimistic: ChatMessage = {
        id: clientId,
        room_id: roomId,
        user: currentUser
          ? { id: currentUser.id, username: currentUser.username, avatar_url: currentUser.avatar_url }
          : { id: clientId, username: "you" },
        content,
        message_type: "text",
        created_at: now,
        updated_at: now,
        read_by: [],
        reactions: [],
        status: "sending",
      };

      queryClient.setQueryData<MessagesData>(queryKey, (old) =>
        upsertMessages(old, [{ message: optimistic }], currentUserId),
      );

      const sentOverWs = send({ type: "message.send", room_id: roomId, content, client_id: clientId });

      if (sentOverWs) {
        // Success is reconciled centrally when message.new echoes this client_id
        // back. This timer only bites if that echo never arrives (send lost mid-
        // drop); it self-checks, so it's a no-op once the bubble is replaced.
        setTimeout(() => {
          queryClient.setQueryData<MessagesData>(queryKey, (old) => markFailed(old, clientId));
        }, WS_ACK_TIMEOUT_MS);
        return;
      }

      api
        .post<MessageWithAuthor>(
          `/api/v1/rooms/${roomId}/messages`,
          { content },
          { headers: { "Idempotency-Key": clientId } },
        )
        .then((serverMessage) => {
          queryClient.setQueryData<MessagesData>(queryKey, (old) =>
            upsertMessages(old, [{ message: serverMessage, clientId }], currentUserId),
          );
        })
        .catch(() => {
          queryClient.setQueryData<MessagesData>(queryKey, (old) => markFailed(old, clientId));
        });
    },
    [queryClient, currentUser, send, roomId],
  );
}
