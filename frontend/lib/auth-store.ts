import { create } from "zustand";
import { persist } from "zustand/middleware";

import type { User } from "./types";

interface AuthState {
  user: User | null;
  token: string | null;
  // The opaque refresh token (Phase 3). Kept in the same store/localStorage
  // key as the access token — see plan.md's Phase 3 proposal for why this
  // stays localStorage rather than moving to an httpOnly cookie.
  refreshToken: string | null;
  // False on the server and on the very first client render, flips true
  // once zustand/middleware's persist has actually read localStorage — lets
  // route guards tell "not logged in" from "haven't checked yet" apart, so
  // they don't redirect on a false negative before hydration runs.
  hasHydrated: boolean;
  setAuth: (user: User, token: string, refreshToken: string) => void;
  clearAuth: () => void;
  setHasHydrated: (value: boolean) => void;
}

// Single source of truth for the persisted session (see plan.md: JWT in
// localStorage, not a cookie). lib/api.ts reads the token from this store
// via getState() rather than keeping its own copy in localStorage.
export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      token: null,
      refreshToken: null,
      hasHydrated: false,
      setAuth: (user, token, refreshToken) => set({ user, token, refreshToken }),
      clearAuth: () => set({ user: null, token: null, refreshToken: null }),
      setHasHydrated: (value) => set({ hasHydrated: value }),
    }),
    {
      name: "convoychat-auth",
      partialize: (state) => ({ user: state.user, token: state.token, refreshToken: state.refreshToken }),
      onRehydrateStorage: () => (state) => {
        state?.setHasHydrated(true);
      },
    },
  ),
);
