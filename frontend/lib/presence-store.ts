import { create } from "zustand";

import type { PresenceStatus } from "./types";

// Live presence, keyed by user id. Populated in Phase 13 by the WebSocket
// provider's routing of user.status_changed / user.joined; *rendered* in
// Phase 14 (avatar dots, members list). Kept in Zustand rather than React
// Query because presence is ephemeral, high-churn, and read by many small
// components at once — the same reasoning that put the session in auth-store.
interface PresenceEntry {
  status: PresenceStatus;
  lastSeenAt?: string;
}

interface PresenceState {
  statuses: Record<string, PresenceEntry>;
  // The current user's *chosen* status (the self-presence control). Distinct
  // from statuses[currentUserId] — that's what the server echoes back, which
  // the backend resets to "online" on every fresh connect (see
  // internal/store/presence.go's PresenceConnect) and can race the room
  // subscription on the way back to us anyway. This is the authoritative local
  // intent: it drives the current user's own displayed dot (usePresence) and
  // is re-asserted to the server on every (re)connect (useWebSocket's onopen)
  // so an "away" the user picked isn't silently lost when the socket blips.
  // Session-only (not persisted) — a fresh page load is a fresh connect, which
  // the backend treats as "online" too.
  selfStatus: PresenceStatus;
  setStatus: (userId: string, status: PresenceStatus, lastSeenAt?: string) => void;
  setSelfStatus: (status: PresenceStatus) => void;
  clear: () => void;
}

export const usePresenceStore = create<PresenceState>((set) => ({
  statuses: {},
  selfStatus: "online",
  setStatus: (userId, status, lastSeenAt) =>
    set((state) => ({
      statuses: { ...state.statuses, [userId]: { status, lastSeenAt } },
    })),
  setSelfStatus: (status) => set({ selfStatus: status }),
  clear: () => set({ statuses: {}, selfStatus: "online" }),
}));
