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
  setStatus: (userId: string, status: PresenceStatus, lastSeenAt?: string) => void;
  clear: () => void;
}

export const usePresenceStore = create<PresenceState>((set) => ({
  statuses: {},
  setStatus: (userId, status, lastSeenAt) =>
    set((state) => ({
      statuses: { ...state.statuses, [userId]: { status, lastSeenAt } },
    })),
  clear: () => set({ statuses: {} }),
}));
