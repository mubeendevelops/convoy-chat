"use client";

import { useCallback, useMemo, useRef } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";

import { ToastAction } from "@/components/ui/toast";
import { toast } from "@/hooks/use-toast";
import { useWebSocket } from "@/hooks/useWebSocket";
import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import {
  findMessage,
  flattenSortedDedup,
  markFailed,
  markSending,
  messagesQueryKey,
  nextCursor,
  PAGE_SIZE,
  upsertMessages,
  type ChatMessage,
  type MessagesData,
  type MessagesPage,
} from "@/lib/messagesCache";
import type { MessageWithAuthor, ToggleReactionResponse } from "@/lib/types";

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

  // Memoized on query.data specifically (not recomputed on every render of
  // whatever calls useMessages): flattenSortedDedup always builds a fresh
  // array, and an unmemoized call would hand MessageList a new `messages`
  // reference even on renders where the cache didn't change — e.g. the
  // instant isFetchingNextPage flips true, before the next-older page has
  // actually landed. MessageList's scroll-position-preservation effect keys
  // off `messages` identity to know when a real prepend happened, so that
  // spurious reference change made it consume its restore-anchor a render
  // early, against a scrollHeight that hadn't grown yet (found via Phase 15's
  // virtualization scroll-preservation test — see plan.md).
  const messages = useMemo(() => flattenSortedDedup(query.data), [query.data]);
  return { ...query, messages };
}

// Sends a message, preferring the live WebSocket and falling back to REST when
// the socket is down. Either way an optimistic bubble appears immediately:
//   - WS path: message.send goes out with a client_id nonce; the server echoes
//     it in message.new, and the WS provider reconciles the bubble centrally.
//     An ack timeout flips a never-echoed bubble to "failed".
//   - REST path (socket down): POST with Idempotency-Key, reconciled on success
//     and marked "failed" on error — the unchanged Phase 12 behavior.
// A failure either way surfaces a toast (with its own Retry action) and marks
// the bubble "failed" in place, where MessageBubble also renders a persistent
// Retry control — the toast auto-dismisses after a few seconds, so it can't be
// the only way back for a message someone doesn't notice failed until later.
// Returns { send, retry } — no pending state, so sends are rapid-fire.
export function useSendMessage(roomId: string) {
  const queryClient = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);
  const { send: sendEvent } = useWebSocket();
  // performSend's failure path needs to hand the toast's Retry button a way
  // to call retry() — but retry() itself calls performSend, so a plain
  // useCallback dependency would be circular. A ref sidesteps that: it always
  // points at the latest retry closure without performSend needing to depend
  // on it.
  const retryRef = useRef<(clientId: string, content: string) => void>(() => {});

  const performSend = useCallback(
    (content: string, clientId: string) => {
      const queryKey = messagesQueryKey(roomId);

      const onFail = () => {
        queryClient.setQueryData<MessagesData>(queryKey, (old) => markFailed(old, clientId));
        toast({
          variant: "destructive",
          title: "Message failed to send",
          description: "Check your connection and try again.",
          action: (
            <ToastAction altText="Retry" onClick={() => retryRef.current(clientId, content)}>
              Retry
            </ToastAction>
          ),
        });
      };

      const sentOverWs = sendEvent({ type: "message.send", room_id: roomId, content, client_id: clientId });

      if (sentOverWs) {
        // Success is reconciled centrally when message.new echoes this client_id
        // back. This timer only bites if that echo never arrives (send lost mid-
        // drop) — checking the bubble is still "sending" first stops it from
        // firing a bogus failure toast for a send that actually landed just
        // before the timeout elapsed.
        setTimeout(() => {
          const data = queryClient.getQueryData<MessagesData>(queryKey);
          if (findMessage(data, clientId)?.status === "sending") onFail();
        }, WS_ACK_TIMEOUT_MS);
        return;
      }

      const currentUserId = currentUser?.id ?? clientId;
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
        .catch(onFail);
    },
    [queryClient, roomId, sendEvent, currentUser],
  );

  // Re-attempts a failed bubble in place: same clientId (so a REST retry
  // reuses the original Idempotency-Key rather than risking a double insert)
  // and same content, flipped back to "sending" first. A no-op if the bubble
  // isn't actually in "failed" state anymore (e.g. a resync reconciled it in
  // the background before the click registered) — retrying would otherwise
  // fire a genuine duplicate send for a message that already went through.
  const retry = useCallback(
    (clientId: string, content: string) => {
      const queryKey = messagesQueryKey(roomId);
      const data = queryClient.getQueryData<MessagesData>(queryKey);
      if (findMessage(data, clientId)?.status !== "failed") return;
      queryClient.setQueryData<MessagesData>(queryKey, (old) => markSending(old, clientId));
      performSend(content, clientId);
    },
    [queryClient, roomId, performSend],
  );
  retryRef.current = retry;

  const send = useCallback(
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

      performSend(content, clientId);
    },
    [queryClient, currentUser, roomId, performSend],
  );

  return { send, retry };
}

// Toggles a reaction on a message. Reacting has no client→server WS event
// (REST-only to trigger — see CLAUDE.md's WebSocket event contract), but the
// resulting add/remove broadcasts back over the socket like any other room
// event, even for our own toggle (this app's deliver-on-receive-only
// design). So this doesn't touch the message cache itself — it just fires
// the request and surfaces a toast on failure (e.g. reacting to a message
// that was deleted moments ago, or a network error); the live
// message.reaction event (routed centrally in useWebSocket's routeEvent) is
// what actually updates the UI, the same way a read receipt already works.
export function useToggleReaction() {
  return useCallback((messageId: string, emoji: string) => {
    api.post<ToggleReactionResponse>(`/api/v1/messages/${messageId}/reactions`, { emoji }).catch(() => {
      toast({
        variant: "destructive",
        title: "Couldn't react to message",
        description: "Check your connection and try again.",
      });
    });
  }, []);
}
