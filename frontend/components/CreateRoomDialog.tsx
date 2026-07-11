"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { Loader2, X } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuth } from "@/hooks/useAuth";
import { useCreateRoom, useSearchUsers } from "@/hooks/useRooms";
import { ApiError } from "@/lib/api";
import { validateRoomName } from "@/lib/validation";
import type { UserSummary } from "@/lib/types";

type RoomKind = "channel" | "direct" | "group";

// A group needs at least this many members beyond the creator — mirrors the
// backend's minGroupMembers (see CLAUDE.md's roles-and-groups entry).
// Otherwise "group" is just a worse-UX path to what a direct room's
// auto-dedup already does better for a real 1:1.
const MIN_GROUP_MEMBERS = 2;
const SEARCH_DEBOUNCE_MS = 300;

export function CreateRoomDialog() {
  const router = useRouter();
  const { user } = useAuth();
  const createRoom = useCreateRoom();

  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<RoomKind>("channel");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  // Group member picker: a staged list built up by searching + clicking
  // results, submitted as member_ids on create. Not reusing
  // InviteMemberDialog's click-to-invite-immediately pattern since group
  // creation needs "stage several, then submit once".
  const [groupMembers, setGroupMembers] = useState<UserSummary[]>([]);
  // DM peer: a single selected user (by username search, not a pasted UUID),
  // whose id becomes peer_user_id on create.
  const [directPeer, setDirectPeer] = useState<UserSummary | null>(null);
  // One username-search box, shared by the group and DM pickers (only one kind
  // is active at a time). Backed by useSearchUsers below.
  const [userSearchInput, setUserSearchInput] = useState("");
  const [debouncedUserSearch, setDebouncedUserSearch] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedUserSearch(userSearchInput), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [userSearchInput]);

  // Unscoped by room_id (no room exists yet, unlike InviteMemberDialog's
  // search) — the backend already excludes the caller; staged group members
  // are filtered out client-side below.
  const userSearch = useSearchUsers(debouncedUserSearch);
  const stagedIds = new Set(groupMembers.map((m) => m.id));
  const groupResults = (userSearch.data ?? []).filter((u) => !stagedIds.has(u.id) && u.id !== user?.id);
  const directResults = (userSearch.data ?? []).filter((u) => u.id !== user?.id);

  function clearUserSearch() {
    setUserSearchInput("");
    setDebouncedUserSearch("");
  }

  function reset() {
    setKind("channel");
    setName("");
    setDescription("");
    setNameError(null);
    setFormError(null);
    setGroupMembers([]);
    setDirectPeer(null);
    clearUserSearch();
  }

  // Switching room type clears any in-progress people-picking so the shared
  // search box + results never carry over between the group and DM tabs.
  function changeKind(next: RoomKind) {
    setKind(next);
    setFormError(null);
    setGroupMembers([]);
    setDirectPeer(null);
    clearUserSearch();
  }

  function handleOpenChange(next: boolean) {
    setOpen(next);
    if (!next) reset();
  }

  function addGroupMember(u: UserSummary) {
    setGroupMembers((prev) => [...prev, u]);
    clearUserSearch();
  }

  function removeGroupMember(userId: string) {
    setGroupMembers((prev) => prev.filter((m) => m.id !== userId));
  }

  function selectDirectPeer(u: UserSummary) {
    setDirectPeer(u);
    clearUserSearch();
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setFormError(null);

    try {
      if (kind === "channel") {
        const error = validateRoomName(name);
        setNameError(error);
        if (error) return;

        const room = await createRoom.mutateAsync({
          type: "channel",
          name,
          description: description || undefined,
        });
        handleOpenChange(false);
        router.push(`/chat/${room.id}`);
      } else if (kind === "group") {
        const error = validateRoomName(name);
        setNameError(error);
        if (error) return;
        if (groupMembers.length < MIN_GROUP_MEMBERS) return; // submit is disabled in this state anyway

        const room = await createRoom.mutateAsync({
          type: "group",
          name,
          description: description || undefined,
          member_ids: groupMembers.map((m) => m.id),
        });
        handleOpenChange(false);
        router.push(`/chat/${room.id}`);
      } else {
        if (!directPeer) return; // submit is disabled in this state anyway

        const room = await createRoom.mutateAsync({
          type: "direct",
          peer_user_id: directPeer.id,
        });
        handleOpenChange(false);
        router.push(`/chat/${room.id}`);
      }
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Something went wrong. Please try again.");
    }
  }

  const canSubmitDirect = !!directPeer;
  const canSubmitGroup = groupMembers.length >= MIN_GROUP_MEMBERS;
  const isPending = createRoom.isPending;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button className="w-full">New room</Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>Create a room</DialogTitle>
            <DialogDescription>Start a channel, a group, or message someone directly.</DialogDescription>
          </DialogHeader>

          <div className="flex gap-2 py-4" role="group" aria-label="Room type">
            <Button
              type="button"
              variant={kind === "channel" ? "default" : "outline"}
              aria-pressed={kind === "channel"}
              className="flex-1"
              onClick={() => changeKind("channel")}
            >
              Channel
            </Button>
            <Button
              type="button"
              variant={kind === "group" ? "default" : "outline"}
              aria-pressed={kind === "group"}
              className="flex-1"
              onClick={() => changeKind("group")}
            >
              Group
            </Button>
            <Button
              type="button"
              variant={kind === "direct" ? "default" : "outline"}
              aria-pressed={kind === "direct"}
              className="flex-1"
              onClick={() => changeKind("direct")}
            >
              Direct Message
            </Button>
          </div>

          {formError && (
            <div className="mb-4 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {formError}
            </div>
          )}

          {kind === "channel" && (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="room-name">Name</Label>
                <Input id="room-name" value={name} onChange={(e) => setName(e.target.value)} />
                {nameError && <p className="text-sm text-destructive">{nameError}</p>}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="room-description">Description (optional)</Label>
                <Textarea
                  id="room-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </div>
            </div>
          )}

          {kind === "group" && (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="group-name">Name</Label>
                <Input id="group-name" value={name} onChange={(e) => setName(e.target.value)} />
                {nameError && <p className="text-sm text-destructive">{nameError}</p>}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="group-description">Description (optional)</Label>
                <Textarea
                  id="group-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </div>

              {groupMembers.length > 0 && (
                <ul className="flex flex-wrap gap-1.5">
                  {groupMembers.map((m) => (
                    <li
                      key={m.id}
                      className="flex items-center gap-1 rounded-full border bg-muted px-2 py-1 text-xs"
                    >
                      {m.username}
                      <button
                        type="button"
                        onClick={() => removeGroupMember(m.id)}
                        aria-label={`Remove ${m.username}`}
                        className="rounded-full hover:text-destructive focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      >
                        <X className="h-3 w-3" />
                      </button>
                    </li>
                  ))}
                </ul>
              )}

              <div className="space-y-1.5">
                <Label htmlFor="group-member-search">
                  Members ({groupMembers.length} added, {MIN_GROUP_MEMBERS} minimum)
                </Label>
                <div className="relative">
                  <Input
                    id="group-member-search"
                    placeholder="Search by username…"
                    value={userSearchInput}
                    onChange={(e) => setUserSearchInput(e.target.value)}
                  />
                  {userSearch.isFetching && (
                    <Loader2 className="absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 animate-spin text-muted-foreground" />
                  )}
                </div>
                {debouncedUserSearch.trim().length > 0 && (
                  <ul className="max-h-40 space-y-1 overflow-y-auto" aria-live="polite">
                    {groupResults.length === 0 && !userSearch.isFetching ? (
                      <li className="px-1 py-2 text-sm text-muted-foreground">No users found.</li>
                    ) : (
                      groupResults.map((u) => (
                        <li key={u.id}>
                          <button
                            type="button"
                            onClick={() => addGroupMember(u)}
                            className="flex w-full items-center gap-2 rounded-md px-1 py-1.5 text-left text-sm hover:bg-muted"
                          >
                            <Avatar className="h-6 w-6 shrink-0">
                              {u.avatar_url && <AvatarImage src={u.avatar_url} alt={u.username} />}
                              <AvatarFallback>{u.username.slice(0, 1).toUpperCase()}</AvatarFallback>
                            </Avatar>
                            <span className="min-w-0 flex-1 truncate">{u.username}</span>
                          </button>
                        </li>
                      ))
                    )}
                  </ul>
                )}
              </div>
            </div>
          )}

          {kind === "direct" && (
            <div className="space-y-1.5">
              <Label htmlFor="dm-user-search">To</Label>
              {directPeer ? (
                <div className="flex items-center gap-2 rounded-md border bg-muted px-3 py-2 text-sm">
                  <Avatar className="h-6 w-6 shrink-0">
                    {directPeer.avatar_url && <AvatarImage src={directPeer.avatar_url} alt={directPeer.username} />}
                    <AvatarFallback>{directPeer.username.slice(0, 1).toUpperCase()}</AvatarFallback>
                  </Avatar>
                  <span className="min-w-0 flex-1 truncate">{directPeer.username}</span>
                  <button
                    type="button"
                    onClick={() => setDirectPeer(null)}
                    aria-label={`Clear ${directPeer.username}`}
                    className="rounded-full hover:text-destructive focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
              ) : (
                <>
                  <div className="relative">
                    <Input
                      id="dm-user-search"
                      placeholder="Search by username…"
                      value={userSearchInput}
                      onChange={(e) => setUserSearchInput(e.target.value)}
                    />
                    {userSearch.isFetching && (
                      <Loader2 className="absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 animate-spin text-muted-foreground" />
                    )}
                  </div>
                  {debouncedUserSearch.trim().length > 0 && (
                    <ul className="max-h-40 space-y-1 overflow-y-auto" aria-live="polite">
                      {directResults.length === 0 && !userSearch.isFetching ? (
                        <li className="px-1 py-2 text-sm text-muted-foreground">No users found.</li>
                      ) : (
                        directResults.map((u) => (
                          <li key={u.id}>
                            <button
                              type="button"
                              onClick={() => selectDirectPeer(u)}
                              className="flex w-full items-center gap-2 rounded-md px-1 py-1.5 text-left text-sm hover:bg-muted"
                            >
                              <Avatar className="h-6 w-6 shrink-0">
                                {u.avatar_url && <AvatarImage src={u.avatar_url} alt={u.username} />}
                                <AvatarFallback>{u.username.slice(0, 1).toUpperCase()}</AvatarFallback>
                              </Avatar>
                              <span className="min-w-0 flex-1 truncate">{u.username}</span>
                            </button>
                          </li>
                        ))
                      )}
                    </ul>
                  )}
                </>
              )}
            </div>
          )}

          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => handleOpenChange(false)}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={
                isPending ||
                (kind === "direct" && !canSubmitDirect) ||
                (kind === "group" && !canSubmitGroup)
              }
            >
              {isPending
                ? "Creating..."
                : kind === "channel"
                  ? "Create channel"
                  : kind === "group"
                    ? "Create group"
                    : "Start DM"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
