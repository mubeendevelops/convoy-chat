"use client";

import { useInfiniteQuery, useMutation, useQueryClient, type InfiniteData } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import type { MessageWithAuthor } from "@/lib/types";

const PAGE_SIZE = 50;

// A message still in flight or that failed to send carries a client-only
// `status`; a confirmed server message never has one (the field is optional,
// so real API responses satisfy this type as-is). Kept here rather than in
// lib/types.ts, since that file mirrors the backend 1:1 and this is UI-only
// state layered on top.
export type ChatMessage = MessageWithAuthor & { status?: "sending" | "failed" };

type MessagesPage = ChatMessage[];

function messagesQueryKey(roomId: string | undefined) {
  return ["messages", roomId] as const;
}

// Pages arrive newest-first (see CLAUDE.md keyset pagination). The cursor
// for the next (older) page is the created_at of the last element of the
// last page fetched — a short page means there's nothing older left.
function nextCursor(page: MessagesPage): string | undefined {
  return page.length < PAGE_SIZE ? undefined : page[page.length - 1]?.created_at;
}

// Paginated room history, newest at the bottom. Call fetchNextPage() when
// the user scrolls to the top of the list to load the next-older page.
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

  // Each page is newest-first and pages are fetched newest-page-first, so
  // flattening alone would already be newest-to-oldest — but re-sorting
  // explicitly (ascending by created_at, id as a tiebreak) keeps ordering
  // correct and stable once optimistic sends merge in below, rather than
  // relying on fetch order.
  const messages: ChatMessage[] = (query.data?.pages.flat() ?? [])
    .slice()
    .sort((a, b) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id));

  return { ...query, messages };
}

interface SendMessageVars {
  content: string;
  clientId: string;
}

// REST fallback send (Phase 12 is REST-only; WebSocket send arrives in
// Phase 13). onMutate appends an optimistic message immediately; onSuccess
// swaps it for the server's version; onError marks it "failed" in place
// instead of silently dropping it, so the user's text and the failure both
// stay visible. clientId doubles as the Idempotency-Key header, so a
// double-invoke (double-click, retry) can't insert the message twice.
export function useSendMessage(roomId: string) {
  const queryClient = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);
  const queryKey = messagesQueryKey(roomId);

  return useMutation<MessageWithAuthor, unknown, SendMessageVars, { clientId: string }>({
    mutationFn: ({ content, clientId }) =>
      api.post<MessageWithAuthor>(
        `/api/v1/rooms/${roomId}/messages`,
        { content },
        { headers: { "Idempotency-Key": clientId } },
      ),

    onMutate: async ({ content, clientId }) => {
      await queryClient.cancelQueries({ queryKey });

      const optimisticMessage: ChatMessage = {
        id: clientId,
        room_id: roomId,
        user: currentUser
          ? { id: currentUser.id, username: currentUser.username, avatar_url: currentUser.avatar_url }
          : { id: clientId, username: "you" },
        content,
        message_type: "text",
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
        read_by: [],
        reactions: [],
        status: "sending",
      };

      queryClient.setQueryData<InfiniteData<MessagesPage, string | undefined>>(queryKey, (old) =>
        old
          ? { ...old, pages: [[optimisticMessage, ...(old.pages[0] ?? [])], ...old.pages.slice(1)] }
          : { pages: [[optimisticMessage]], pageParams: [undefined] },
      );

      return { clientId };
    },

    onSuccess: (serverMessage, _vars, context) => {
      queryClient.setQueryData<InfiniteData<MessagesPage, string | undefined>>(queryKey, (old) =>
        old
          ? {
              ...old,
              pages: old.pages.map((page) => page.map((m) => (m.id === context.clientId ? serverMessage : m))),
            }
          : old,
      );
    },

    onError: (_err, _vars, context) => {
      if (!context) return;
      queryClient.setQueryData<InfiniteData<MessagesPage, string | undefined>>(queryKey, (old) =>
        old
          ? {
              ...old,
              pages: old.pages.map((page) =>
                page.map((m) => (m.id === context.clientId ? { ...m, status: "failed" as const } : m)),
              ),
            }
          : old,
      );
    },
  });
}
