"use client";

import { useEffect, useRef, type RefObject } from "react";

import { useWebSocket } from "@/hooks/useWebSocket";
import { useAuthStore } from "@/lib/auth-store";
import type { ChatMessage } from "@/lib/messagesCache";

// Fraction of a message bubble that must be visible within the scroll
// container before it counts as "read".
const VISIBILITY_THRESHOLD = 0.6;

// Marks messages read (WS message.read) as they scroll into view. Observes
// containerRef's children carrying data-message-id (MessageBubble's root),
// limited to messages that are: not our own, not deleted, not a
// still-optimistic/failed local bubble (no real id to send yet), and not
// already in read_by — either from a previous session (the REST history
// already reflects it) or already reported this mount (sentRef).
export function useReadReceipts(containerRef: RefObject<HTMLElement>, messages: ChatMessage[]) {
  const { send } = useWebSocket();
  const currentUserId = useAuthStore((s) => s.user?.id);
  const sentRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    const root = containerRef.current;
    if (!root || !currentUserId) return;

    const eligibleIds = new Set(
      messages
        .filter(
          (m) =>
            m.user.id !== currentUserId &&
            !m.deleted_at &&
            !m.status &&
            !m.read_by.includes(currentUserId) &&
            !sentRef.current.has(m.id),
        )
        .map((m) => m.id),
    );
    if (eligibleIds.size === 0) return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          const messageId = (entry.target as HTMLElement).dataset.messageId;
          if (!messageId || sentRef.current.has(messageId)) continue;
          sentRef.current.add(messageId);
          send({ type: "message.read", message_id: messageId });
          observer.unobserve(entry.target);
        }
      },
      { root, threshold: VISIBILITY_THRESHOLD },
    );

    eligibleIds.forEach((id) => {
      const el = root.querySelector<HTMLElement>(`[data-message-id="${id}"]`);
      if (el) observer.observe(el);
    });

    return () => observer.disconnect();
  }, [containerRef, messages, currentUserId, send]);
}
