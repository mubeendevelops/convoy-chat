"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { isValidUuid } from "@/lib/validation";
import type { CreateRoomRequest, Room, RoomDetail, User } from "@/lib/types";

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
