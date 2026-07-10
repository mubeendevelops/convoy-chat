"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useMutation } from "@tanstack/react-query";

import { api, ApiError } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";
import type { AuthResponse, LoginRequest, SignupRequest } from "@/lib/types";

// Auth state + actions. Route protection is separate (useRequireAuth /
// useRequireGuest below) — this hook doesn't navigate on its own; a
// successful login/signup just updates the store, and whichever guard is
// mounted on the current page reacts to that.
export function useAuth() {
  const user = useAuthStore((s) => s.user);
  const token = useAuthStore((s) => s.token);
  const hasHydrated = useAuthStore((s) => s.hasHydrated);
  const setAuth = useAuthStore((s) => s.setAuth);
  const clearAuth = useAuthStore((s) => s.clearAuth);

  const loginMutation = useMutation<AuthResponse, ApiError, LoginRequest>({
    mutationFn: (body) => api.post<AuthResponse>("/api/v1/auth/login", body, { auth: false }),
    onSuccess: (data) => setAuth(data.user, data.token, data.refresh_token),
  });

  const signupMutation = useMutation<AuthResponse, ApiError, SignupRequest>({
    mutationFn: (body) => api.post<AuthResponse>("/api/v1/auth/signup", body, { auth: false }),
    onSuccess: (data) => setAuth(data.user, data.token, data.refresh_token),
  });

  // Best-effort server-side revoke (Phase 3: refresh tokens) before clearing
  // the local session — revokes the whole refresh-token family so a copy of
  // it sitting anywhere else can't be used to silently re-auth. A network
  // failure here shouldn't block logging out locally, so it's swallowed
  // rather than surfaced; clearAuth() always runs.
  async function logout() {
    const refreshToken = useAuthStore.getState().refreshToken;
    if (refreshToken) {
      try {
        await api.post("/api/v1/auth/logout", { refresh_token: refreshToken });
      } catch {
        // Ignored — see comment above.
      }
    }
    clearAuth();
  }

  return {
    user,
    isAuthenticated: !!token,
    isHydrated: hasHydrated,
    login: loginMutation.mutateAsync,
    isLoggingIn: loginMutation.isPending,
    signup: signupMutation.mutateAsync,
    isSigningUp: signupMutation.isPending,
    logout,
  };
}

// For pages that require a logged-in user (e.g. everything under /chat).
// Redirects to /login once hydration has confirmed there's no session;
// isReady tells the caller when it's safe to render the real content.
export function useRequireAuth() {
  const { isAuthenticated, isHydrated } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isHydrated && !isAuthenticated) {
      router.replace("/login");
    }
  }, [isHydrated, isAuthenticated, router]);

  return { isReady: isHydrated && isAuthenticated, isHydrated };
}

// For pages only meant for logged-out visitors (/login, /signup). Redirects
// to /chat once hydration has confirmed there's already a session.
export function useRequireGuest() {
  const { isAuthenticated, isHydrated } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isHydrated && isAuthenticated) {
      router.replace("/chat");
    }
  }, [isHydrated, isAuthenticated, router]);

  return { isReady: isHydrated && !isAuthenticated, isHydrated };
}

// For the admin dashboard (/admin, Phase 3 post-v1). Redirects to /chat once
// hydration has confirmed the caller either isn't logged in or isn't a
// system admin — client-side UX only, same "server is the real gate"
// philosophy as every other route guard here; every /admin/* endpoint is
// independently enforced by RequireSystemAdmin regardless of what this hook
// does.
export function useRequireSystemAdmin() {
  const { user, isAuthenticated, isHydrated } = useAuth();
  const router = useRouter();
  const isSystemAdmin = !!user?.is_system_admin;

  useEffect(() => {
    if (isHydrated && (!isAuthenticated || !isSystemAdmin)) {
      router.replace("/chat");
    }
  }, [isHydrated, isAuthenticated, isSystemAdmin, router]);

  return { isReady: isHydrated && isAuthenticated && isSystemAdmin, isHydrated };
}
