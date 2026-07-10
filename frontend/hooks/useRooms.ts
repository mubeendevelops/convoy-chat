"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

import { toast } from "@/hooks/use-toast";
import { api } from "@/lib/api";
import { isValidUuid } from "@/lib/validation";
import type { CreateRoomRequest, LeaveRoomResponse, Room, RoomDetail, User } from "@/lib/types";

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
