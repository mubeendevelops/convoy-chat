"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

import { toast } from "@/hooks/use-toast";
import { api } from "@/lib/api";
import { isValidUuid } from "@/lib/validation";
import type {
  CreateRoomRequest,
  LeaveRoomResponse,
  Room,
  RoomDetail,
  RoomMember,
  User,
  UserSummary,
} from "@/lib/types";

export function useRooms() {
  return useQuery({
    queryKey: ["rooms"],
    queryFn: () => api.get<Room[]>("/api/v1/rooms"),
  });
}

export function useRoom(roomId: string | undefined) {
  return useQuery({
    queryKey: ["room", roomId],
    queryFn: () => api.get<RoomDetail>(`/api/v1/rooms/${roomId}`),
    enabled: !!roomId,
  });
}

export function useCreateRoom() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateRoomRequest) => api.post<Room>("/api/v1/rooms", body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["rooms"] });
    },
  });
}

// Leaves a room via POST /rooms/{id}/leave (backend sets left_at; a second
// leave 404s — see CLAUDE.md). On success the now-inaccessible room's caches
// are dropped and the sidebar list is invalidated so the room disappears from
// it; the caller navigates out of the room view. A failure toasts here (kept
// out of the component) and rejects, so the caller can leave its confirm
// dialog open. Note the *membership* leave is distinct from the WS live-stream
// room.leave — that one still fires on its own when ChatWindow unmounts after
// the navigation.
export function useLeaveRoom() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (roomId: string) => api.post<LeaveRoomResponse>(`/api/v1/rooms/${roomId}/leave`),
    onSuccess: (_data, roomId) => {
      queryClient.removeQueries({ queryKey: ["room", roomId] });
      queryClient.removeQueries({ queryKey: ["messages", roomId] });
      queryClient.invalidateQueries({ queryKey: ["rooms"] });
    },
    onError: () => {
      toast({
        variant: "destructive",
        title: "Couldn't leave the room",
        description: "Check your connection and try again.",
      });
    },
  });
}

// Username-prefix search for the invite picker, backed by GET
// /users/search. `query` is expected already-debounced by the caller (this is
// a plain query hook, like useLookupUser) — the queryKey includes it so each
// distinct debounced value is its own cache entry. Disabled on an empty query
// so clearing the box doesn't fire a request. `roomId` scopes the search so the
// backend omits people already in the room; it's part of the key so results
// don't bleed between rooms. A short staleTime avoids refetching while the user
// backspaces over previously-typed prefixes.
export function useSearchUsers(query: string, roomId?: string) {
  const trimmed = query.trim();
  return useQuery({
    queryKey: ["user-search", trimmed, roomId ?? null],
    queryFn: () =>
      api.get<UserSummary[]>(
        `/api/v1/users/search?q=${encodeURIComponent(trimmed)}` +
          (roomId ? `&room_id=${encodeURIComponent(roomId)}` : ""),
      ),
    enabled: trimmed.length > 0,
    staleTime: 30_000,
  });
}

// Invites a user into a room via POST /rooms/{id}/invite (admin-only,
// enforced server-side; the picker is only shown to admins). On success both
// the room detail (so the members list reflects the new member live for the
// inviter) and any cached user-search results (so the freshly-added user drops
// out of the picker) are invalidated. Errors are surfaced by the caller via
// the returned mutation so it can show them inline next to the picker.
export function useInviteMember(roomId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      api.post<RoomMember>(`/api/v1/rooms/${roomId}/invite`, { user_id: userId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["room", roomId] });
      queryClient.invalidateQueries({ queryKey: ["user-search"] });
    },
  });
}

// Live preview for the DM peer picker: resolves a pasted user ID to a
// username before the caller commits to creating the room. Only fires for a
// syntactically valid UUID — no debounce needed since a pasted ID arrives as
// one complete value, and a hand-typed one just stays disabled mid-entry.
export function useLookupUser(userId: string) {
  return useQuery({
    queryKey: ["user-lookup", userId],
    queryFn: () => api.get<User>(`/api/v1/users/${userId}`),
    enabled: isValidUuid(userId),
    retry: false,
  });
}
