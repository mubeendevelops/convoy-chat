"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { useWebSocket } from "@/hooks/useWebSocket";
import { useAuthStore } from "@/lib/auth-store";

// Locked (asked): 3s of keystroke inactivity before sending typing.stop —
// comfortably under the backend's fixed 5s auto-expire safety net
// (internal/websocket/typing.go), so an explicit stop almost always beats the
// server timeout. The same value doubles as the refresh interval for
// re-sending typing.start while the user keeps typing past it: typing.start
// (re)starts that same 5s server-side timer on every call, so without a
// periodic refresh a long uninterrupted typing burst would go silent for
// other clients at the 5s mark even though the user never paused.
const TYPING_IDLE_MS = 3000;

// Debounced local-typing signaling (notifyTyping/stopTyping, for the
// composer) plus inbound tracking of who else is typing in roomId (via
// user.typing events). One hook covers both directions since they share the
// same room-scoped subscription lifecycle.
export function useTyping(roomId: string) {
  const { send, subscribe } = useWebSocket();
  const currentUserId = useAuthStore((s) => s.user?.id);

  const [typingUserIds, setTypingUserIds] = useState<string[]>([]);
  const activeRef = useRef(false);
  const lastStartSentRef = useRef(0);
  const idleTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopTyping = useCallback(() => {
    if (idleTimerRef.current) {
      clearTimeout(idleTimerRef.current);
      idleTimerRef.current = null;
    }
    if (activeRef.current) {
      activeRef.current = false;
      send({ type: "typing.stop", room_id: roomId });
    }
  }, [send, roomId]);

  const notifyTyping = useCallback(() => {
    const now = Date.now();
    if (!activeRef.current || now - lastStartSentRef.current > TYPING_IDLE_MS) {
      send({ type: "typing.start", room_id: roomId });
      lastStartSentRef.current = now;
      activeRef.current = true;
    }
    if (idleTimerRef.current) clearTimeout(idleTimerRef.current);
    idleTimerRef.current = setTimeout(stopTyping, TYPING_IDLE_MS);
  }, [send, roomId, stopTyping]);

  useEffect(() => {
    return subscribe("user.typing", (event) => {
      if (event.room_id !== roomId || event.user_id === currentUserId) return;
      setTypingUserIds((prev) => {
        const isTracked = prev.includes(event.user_id);
        if (event.is_typing) return isTracked ? prev : [...prev, event.user_id];
        return isTracked ? prev.filter((id) => id !== event.user_id) : prev;
      });
    });
  }, [subscribe, roomId, currentUserId]);

  // Room switch/unmount: don't leave a dangling typing.start hanging for up
  // to 5s after the composer disappears.
  useEffect(() => {
    return () => {
      stopTyping();
      setTypingUserIds([]);
    };
  }, [roomId, stopTyping]);

  return { typingUserIds, notifyTyping, stopTyping };
}
