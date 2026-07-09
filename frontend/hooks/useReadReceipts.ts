"use client";

import { useCallback, useEffect, useRef } from "react";

import { useWebSocket } from "@/hooks/useWebSocket";
import { useAuthStore } from "@/lib/auth-store";
import type { ChatMessage } from "@/lib/messagesCache";

// Fraction of a message bubble that must be visible within the scroll
// container before it counts as "read".
const VISIBILITY_THRESHOLD = 0.6;

// Marks messages read (WS message.read) as they scroll into view.
//
// Returns observeMessage(el, messageId), which each rendered row attaches to
// its own root element (see MessageList). That per-row-registers-itself
// design (rather than this hook querying the DOM for every eligible message
// whenever `messages` changes) is what makes it work correctly under
// MessageList's virtualization (Phase 15): only rows currently mounted in the
// DOM can be observed at all, and a virtualizer mounts/unmounts rows as the
// user scrolls *without* `messages` itself changing. A historical message
// scrolled back into view re-mounts (and its ref fires again) exactly like a
// brand-new live one, so it re-registers the same way — nothing special
// needed for the "scrolled away and back" case.
//
// Eligibility (checked at observe time, against a ref so it's always current
// without needing to reconstruct the observer): not our own, not deleted,
// not a still-optimistic/failed local bubble (no real id to send yet), and
// not already in read_by — either from a previous session (the REST history
// already reflects it) or already reported this mount (sentRef).
export function useReadReceipts(container: HTMLElement | null, messages: ChatMessage[]) {
  const { send } = useWebSocket();
  const currentUserId = useAuthStore((s) => s.user?.id);
  const sentRef = useRef<Set<string>>(new Set());
  const observerRef = useRef<IntersectionObserver | null>(null);
  // Maps each observed element back to its message id. The element
  // MessageList actually observes is its virtualized row wrapper (needed for
  // measureElement too), which doesn't itself carry data-message-id — only
  // MessageBubble's nested root does — so this avoids depending on which
  // element in that subtree the observer happens to be watching.
  const targetIdsRef = useRef<WeakMap<Element, string>>(new WeakMap());

  const messagesRef = useRef(messages);
  messagesRef.current = messages;

  // One observer per mounted room, recreated only if the container element
  // itself changes — not on every messages update; rows attach to it
  // individually as they mount. Takes the element as a plain value (not a
  // ref object) deliberately: MessageList's scroll container doesn't exist
  // yet during the initial loading render (it's a different, simpler JSX
  // branch), and a ref *object*'s identity never changes when its .current
  // is later attached — an effect depending on the ref object itself would
  // run once too early (while still null), bail out, and never get a chance
  // to re-run once the real element mounts. Depending on the element value
  // works because passing it through React state (see MessageList) makes
  // that transition from null to the real node an actual reactive change.
  useEffect(() => {
    if (!container) return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          const messageId = targetIdsRef.current.get(entry.target);
          if (!messageId || sentRef.current.has(messageId)) continue;
          sentRef.current.add(messageId);
          send({ type: "message.read", message_id: messageId });
          observer.unobserve(entry.target);
        }
      },
      { root: container, threshold: VISIBILITY_THRESHOLD },
    );
    observerRef.current = observer;

    return () => {
      observer.disconnect();
      observerRef.current = null;
    };
  }, [container, send]);

  return useCallback(
    (el: HTMLElement | null, messageId: string) => {
      if (!el || !observerRef.current || !currentUserId) return;
      const message = messagesRef.current.find((m) => m.id === messageId);
      if (!message) return;
      const eligible =
        message.user.id !== currentUserId &&
        !message.deleted_at &&
        !message.status &&
        !message.read_by.includes(currentUserId) &&
        !sentRef.current.has(message.id);
      if (eligible) {
        targetIdsRef.current.set(el, messageId);
        observerRef.current.observe(el);
      }
    },
    [currentUserId],
  );
}
