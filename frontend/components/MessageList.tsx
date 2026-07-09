"use client";

import { useLayoutEffect, useRef } from "react";
import { Loader2, MessageSquare } from "lucide-react";

import { MessageBubble } from "@/components/MessageBubble";
import { Skeleton } from "@/components/ui/skeleton";
import type { ChatMessage } from "@/hooks/useMessages";
import { useReadReceipts } from "@/hooks/useReadReceipts";

interface MessageListProps {
  messages: ChatMessage[];
  currentUserId: string;
  isLoading: boolean;
  isError: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadOlder: () => void;
}

// Scrolling within this many px of the top triggers loading the next-older
// page; within this many px of the bottom counts as "already at the bottom"
// for the auto-scroll-on-new-message decision below.
const LOAD_OLDER_THRESHOLD = 80;
const NEAR_BOTTOM_THRESHOLD = 120;

function isNearBottom(el: HTMLDivElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_THRESHOLD;
}

export function MessageList({
  messages,
  currentUserId,
  isLoading,
  isError,
  hasNextPage,
  isFetchingNextPage,
  onLoadOlder,
}: MessageListProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  // Set right before onLoadOlder fires so the post-prepend layout effect
  // below can restore the scroll offset instead of jumping to the top.
  const prevScrollHeightRef = useRef<number | null>(null);
  const prevMessageCountRef = useRef(0);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    if (prevScrollHeightRef.current !== null) {
      el.scrollTop += el.scrollHeight - prevScrollHeightRef.current;
      prevScrollHeightRef.current = null;
      prevMessageCountRef.current = messages.length;
      return;
    }

    // Jump to the bottom on the very first page rendered, or whenever the
    // list grows while the user was already near the bottom (their own
    // optimistic send; eventually a live WS push in Phase 13). A user who
    // has scrolled up to read history is left alone.
    const grew = messages.length > prevMessageCountRef.current;
    const wasEmpty = prevMessageCountRef.current === 0;
    if (grew && (wasEmpty || isNearBottom(el))) {
      el.scrollTop = el.scrollHeight;
    }
    prevMessageCountRef.current = messages.length;
  }, [messages]);

  useReadReceipts(containerRef, messages);

  function handleScroll() {
    const el = containerRef.current;
    if (!el || isFetchingNextPage || !hasNextPage) return;
    if (el.scrollTop < LOAD_OLDER_THRESHOLD) {
      prevScrollHeightRef.current = el.scrollHeight;
      onLoadOlder();
    }
  }

  if (isLoading) {
    return (
      <div className="flex-1 space-y-4 overflow-y-auto p-6">
        <Skeleton className="h-14 w-2/3" />
        <Skeleton className="ml-auto h-14 w-2/3" />
        <Skeleton className="h-14 w-1/2" />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="flex flex-1 items-center justify-center p-8 text-center">
        <p className="text-sm text-muted-foreground">Couldn&apos;t load messages. Try refreshing.</p>
      </div>
    );
  }

  if (messages.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
        <MessageSquare className="h-10 w-10 text-muted-foreground" />
        <p className="text-sm text-muted-foreground">No messages yet — say hello!</p>
      </div>
    );
  }

  return (
    <div ref={containerRef} onScroll={handleScroll} className="flex-1 overflow-y-auto">
      <div className="flex flex-col gap-4 p-6">
        {isFetchingNextPage && (
          <div className="flex justify-center py-2">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        )}
        {messages.map((message) => (
          <MessageBubble key={message.id} message={message} isOwn={message.user.id === currentUserId} />
        ))}
      </div>
    </div>
  );
}
