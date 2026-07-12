"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

import { toast } from "@/hooks/use-toast";
import { api } from "@/lib/api";
import type {
  ChangeRoleResponse,
  CreateRoomRequest,
  LeaveRoomResponse,
  MemberRole,
  PublicChannel,
  RemoveMemberResponse,
  Room,
  RoomDetail,
  RoomMember,
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

// Total unread across every room the caller is in — drives the mobile
// hamburger badge and the browser-tab-title count. Reads straight off the
// same ["rooms"] cache the sidebar renders, so it updates live as the WS
// provider bumps per-room counts.
export function useUnreadTotal() {
  const { data } = useRooms();
  return (data ?? []).reduce((sum, room) => sum + (room.unread_count ?? 0), 0);
}

// Marks a room read via POST /rooms/{id}/read, advancing the caller's
// server-side last-read cursor. Optimistically zeroes that room's unread_count
// in the ["rooms"] cache immediately (onMutate) so the badge clears the instant
// the room is opened, no refetch needed. A failure is non-critical and stays
// silent: the server cursor just isn't advanced, so the count reappears on the
// next GET /rooms and clears again on the next open. Called from ChatWindow on
// room open and close.
export function useMarkRoomRead() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (roomId: string) => api.post<{ status: string }>(`/api/v1/rooms/${roomId}/read`),
    onMutate: (roomId) => {
      queryClient.setQueryData<Room[]>(["rooms"], (old) =>
        old?.map((room) => (room.id === roomId ? { ...room, unread_count: 0 } : room)),
      );
    },
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

// Public, non-archived channels the caller isn't a member of yet, backed by
// GET /rooms/public. A plain list, no debounced search — fine at v1 scale,
// same call already made for RoomsList's direct-room N+1 lookups.
export function useBrowseChannels(enabled: boolean) {
  return useQuery({
    queryKey: ["rooms", "public"],
    queryFn: () => api.get<PublicChannel[]>("/api/v1/rooms/public"),
    enabled,
  });
}

// Self-joins a public channel via POST /rooms/{id}/join (backend enforces
// is_public — a private or nonexistent room 403s, an already-active member
// 409s). On success both the caller's own room list and the browse list are
// invalidated (the joined channel drops out of one, appears in the other);
// the joiner's own client has no live signal of its own join (user.joined
// only reaches already-open rooms), so cache invalidation is the only way
// their sidebar picks it up. The caller navigates into the room afterward.
export function useJoinChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (roomId: string) => api.post<RoomMember>(`/api/v1/rooms/${roomId}/join`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["rooms"] });
    },
  });
}

// Username-prefix search for the invite / DM / group-member pickers, backed by
// GET /users/search. `query` is expected already-debounced by the caller (a
// plain query hook) — the queryKey includes it so each
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

// Promotes/demotes a member via PATCH /rooms/{id}/members/{user_id}/role
// (admin-only, enforced server-side — the caller decides who sees the
// controls). No optimistic update: the live member.role_changed broadcast
// (routed centrally in useWebSocket's routeEvent) is what actually updates
// the members list, the same "REST call + live broadcast does the work"
// pattern already used for reactions — this mutation just fires the request
// and surfaces a toast on failure (e.g. a stale demote of the room's last
// admin racing another admin's own change, or a network error).
export function useChangeMemberRole(roomId: string) {
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: MemberRole }) =>
      api.patch<ChangeRoleResponse>(`/api/v1/rooms/${roomId}/members/${userId}/role`, { role }),
    onError: () => {
      toast({
        variant: "destructive",
        title: "Couldn't change that member's role",
        description: "Check your connection and try again.",
      });
    },
  });
}

// Kicks a member via DELETE /rooms/{id}/members/{user_id} (admin-only,
// enforced server-side; self-removal is rejected — that's what leaving is
// for). No optimistic update here either: the live user.left broadcast is
// what removes them from an already-open members list for everyone still in
// the room, including — via useWebSocket's self-cleanup — the removed
// member's own client. This mutation still invalidates the room detail
// directly for the *kicker's* own immediate feedback, since their own
// user.left delivery is exactly as live as anyone else's and there's no
// reason to wait on the round trip when the outcome is already known.
export function useRemoveMember(roomId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) => api.delete<RemoveMemberResponse>(`/api/v1/rooms/${roomId}/members/${userId}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["room", roomId] });
    },
    onError: () => {
      toast({
        variant: "destructive",
        title: "Couldn't remove that member",
        description: "Check your connection and try again.",
      });
    },
  });
}
