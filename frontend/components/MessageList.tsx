"use client";

import { memo, useCallback, useLayoutEffect, useRef, useState } from "react";
import { useVirtualizer, type VirtualItem } from "@tanstack/react-virtual";
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
  onRetry: (clientId: string, content: string) => void;
  onToggleReaction: (messageId: string, emoji: string) => void;
}

// Scrolling within this many px of the top triggers loading the next-older
// page; within this many px of the bottom counts as "already at the bottom"
// for the auto-scroll-on-new-message decision below.
const LOAD_OLDER_THRESHOLD = 80;
const NEAR_BOTTOM_THRESHOLD = 120;
// Starting guess for a bubble's height (single-line + header), used only
// until a row actually renders and reports its real measured height —
// wrong values here just cost a slightly-off scrollbar/total-size briefly,
// never a layout bug.
const ESTIMATED_ROW_HEIGHT = 76;

function isNearBottom(el: HTMLDivElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_THRESHOLD;
}

interface RowProps {
  item: VirtualItem;
  message: ChatMessage;
  isOwn: boolean;
  currentUserId: string;
  onRetry: (clientId: string, content: string) => void;
  onToggleReaction: (messageId: string, emoji: string) => void;
  measureElement: (el: Element | null) => void;
  observeMessage: (el: HTMLElement | null, messageId: string) => void;
}

// One virtualized row. Memoized, and its merged ref callback is a useCallback
// keyed on stable inputs (message.id plus the two stable hook-provided
// functions) — without that, a fresh inline ref closure on every render would
// make React detach+reattach it (measurement/observation) on every unrelated
// cache update instead of just the rows that actually changed.
const Row = memo(function Row({
  item,
  message,
  isOwn,
  currentUserId,
  onRetry,
  onToggleReaction,
  measureElement,
  observeMessage,
}: RowProps) {
  const setRefs = useCallback(
    (el: HTMLDivElement | null) => {
      measureElement(el);
      observeMessage(el, message.id);
    },
    [measureElement, observeMessage, message.id],
  );

  return (
    <div
      data-index={item.index}
      ref={setRefs}
      className="absolute inset-x-0 px-6 py-2"
      style={{ transform: `translateY(${item.start}px)` }}
    >
      <MessageBubble
        message={message}
        isOwn={isOwn}
        currentUserId={currentUserId}
        onRetry={onRetry}
        onToggleReaction={onToggleReaction}
      />
    </div>
  );
});

export function MessageList({
  messages,
  currentUserId,
  isLoading,
  isError,
  hasNextPage,
  isFetchingNextPage,
  onLoadOlder,
  onRetry,
  onToggleReaction,
}: MessageListProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  // Mirrors containerRef.current as reactive state, purely so
  // useReadReceipts's effect can depend on *when the node actually mounts*
  // (a ref object's identity never changes, so an effect can't use it to
  // detect that transition — see useReadReceipts for the full story).
  const [containerEl, setContainerEl] = useState<HTMLDivElement | null>(null);
  const setContainerNode = useCallback((el: HTMLDivElement | null) => {
    containerRef.current = el;
    setContainerEl(el);
  }, []);
  // Set right before onLoadOlder fires so the post-prepend layout effect
  // below can restore the scroll offset instead of jumping to the top.
  const prevScrollHeightRef = useRef<number | null>(null);
  const prevMessageCountRef = useRef(0);

  // Renders only the messages currently in (or near) the viewport — a room's
  // history can grow into the hundreds after a few "load older" pages, and
  // this keeps the DOM/React work bounded to what's visible regardless (see
  // plan.md Phase 15). getItemKey is keyed on message id rather than the
  // default raw index specifically because loading older messages *prepends*
  // to the array: every existing message's index shifts, and without a
  // stable key the virtualizer would reattach cached measurements to the
  // wrong rows.
  const virtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => containerRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    getItemKey: (index) => messages[index]?.id ?? index,
    overscan: 8,
  });

  const observeMessage = useReadReceipts(containerEl, messages);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    if (prevScrollHeightRef.current !== null) {
      // Only actually restore once the load-older fetch's own page has
      // landed (messages.length grew) — messages can change reference for
      // an unrelated reason while that fetch is still pending (e.g. a read
      // receipt landing on an existing message), and applying the delta
      // against a scrollHeight that hasn't grown yet would consume this
      // anchor a render early, against the wrong before/after heights.
      if (messages.length > prevMessageCountRef.current) {
        el.scrollTop += el.scrollHeight - prevScrollHeightRef.current;
        prevScrollHeightRef.current = null;
        prevMessageCountRef.current = messages.length;
      } else if (!isFetchingNextPage) {
        // The fetch resolved (or was superseded) without adding anything —
        // drop the stale anchor rather than letting it misfire against a
        // later, unrelated update.
        prevScrollHeightRef.current = null;
      }
      return;
    }

    // Jump to the bottom on the very first page rendered, or whenever the
    // list grows while the user was already near the bottom (their own
    // optimistic send, or a live WS push). A user who has scrolled up to
    // read history is left alone. Total scroll height still comes from the
    // virtualizer's sized spacer div, so this needs no virtualizer-specific
    // API — it's the same math as before virtualization.
    const grew = messages.length > prevMessageCountRef.current;
    const wasEmpty = prevMessageCountRef.current === 0;
    if (grew && (wasEmpty || isNearBottom(el))) {
      el.scrollTop = el.scrollHeight;
    }
    prevMessageCountRef.current = messages.length;
  }, [messages, isFetchingNextPage]);

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
    <div className="relative min-h-0 flex-1">
      {isFetchingNextPage && (
        <div className="pointer-events-none absolute inset-x-0 top-0 z-10 flex justify-center py-2">
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        </div>
      )}
      <div
        ref={setContainerNode}
        onScroll={handleScroll}
        role="log"
        aria-label="Messages"
        className="h-full overflow-y-auto overflow-x-hidden"
      >
        <div className="relative" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const message = messages[item.index];
            if (!message) return null;
            return (
              <Row
                key={item.key}
                item={item}
                message={message}
                isOwn={message.user.id === currentUserId}
                currentUserId={currentUserId}
                onRetry={onRetry}
                onToggleReaction={onToggleReaction}
                measureElement={virtualizer.measureElement}
                observeMessage={observeMessage}
              />
            );
          })}
        </div>
      </div>
    </div>
  );
}
