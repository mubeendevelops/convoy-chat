"use client";

import { useEffect } from "react";

import { useUnreadTotal } from "@/hooks/useRooms";

const BASE_TITLE = "ConvoyChat";

// Prefixes the browser tab title with the total unread count (e.g.
// "(3) ConvoyChat"), so a new message is noticeable from another tab. Renders
// nothing — it's a leaf so subscribing to the unread total re-renders only
// this component, not the chat layout / WebSocket provider it's mounted
// alongside. Mounted in app/chat/layout.tsx.
export function UnreadTitle() {
  const unreadTotal = useUnreadTotal();

  useEffect(() => {
    document.title = unreadTotal > 0 ? `(${unreadTotal}) ${BASE_TITLE}` : BASE_TITLE;
    return () => {
      document.title = BASE_TITLE;
    };
  }, [unreadTotal]);

  return null;
}
