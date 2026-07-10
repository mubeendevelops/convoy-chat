"use client";

import { useEffect, useState } from "react";
import { Loader2, UserPlus } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { toast } from "@/hooks/use-toast";
import { useInviteMember, useSearchUsers } from "@/hooks/useRooms";
import { ApiError } from "@/lib/api";
import type { UserSummary } from "@/lib/types";

// Wait this long after the last keystroke before hitting the search endpoint,
// so typing a name is one request, not one per character.
const SEARCH_DEBOUNCE_MS = 300;

// Admin-only invite picker (rendered from RoomHeader for channels only). Search
// is debounced and scoped to the room so people already in it never appear;
// clicking a result invites them and — via the mutation's cache invalidation —
// refreshes the members list and drops them from subsequent results. Stays open
// so an admin can invite several people in a row.
export function InviteMemberDialog({ roomId, roomName }: { roomId: string; roomName: string }) {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [debounced, setDebounced] = useState("");
  const invite = useInviteMember(roomId);

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(input), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [input]);

  const { data: results, isFetching, isError } = useSearchUsers(debounced, roomId);
  const trimmed = debounced.trim();
  const hasQuery = trimmed.length > 0;

  function handleOpenChange(next: boolean) {
    setOpen(next);
    if (!next) {
      setInput("");
      setDebounced("");
    }
  }

  async function handleInvite(user: UserSummary) {
    try {
      await invite.mutateAsync(user.id);
      toast({ title: `Invited @${user.username}` });
    } catch (err) {
      toast({
        variant: "destructive",
        title: "Couldn't invite that user",
        description: err instanceof ApiError ? err.message : "Check your connection and try again.",
      });
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-2">
          <UserPlus className="h-4 w-4" />
          <span className="hidden sm:inline">Invite</span>
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Invite to {roomName}</DialogTitle>
          <DialogDescription>Search by username and add people to this channel.</DialogDescription>
        </DialogHeader>

        <div className="relative">
          <Input
            autoFocus
            placeholder="Search by username…"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            aria-label="Search users to invite"
          />
          {isFetching && (
            <Loader2 className="absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 animate-spin text-muted-foreground" />
          )}
        </div>

        <div className="mt-2 max-h-72 min-h-[3rem] overflow-y-auto" aria-live="polite">
          {!hasQuery ? (
            <p className="px-1 py-3 text-sm text-muted-foreground">Type a username to search.</p>
          ) : isError ? (
            <p className="px-1 py-3 text-sm text-destructive">Couldn&apos;t search users. Try again.</p>
          ) : results && results.length > 0 ? (
            <ul className="space-y-1">
              {results.map((user) => {
                const inviting = invite.isPending && invite.variables === user.id;
                return (
                  <li key={user.id} className="flex items-center gap-3 rounded-md px-1 py-1.5">
                    <Avatar className="h-8 w-8 shrink-0">
                      {user.avatar_url && <AvatarImage src={user.avatar_url} alt={user.username} />}
                      <AvatarFallback>{user.username.slice(0, 1).toUpperCase()}</AvatarFallback>
                    </Avatar>
                    <span className="min-w-0 flex-1 truncate text-sm">{user.username}</span>
                    <Button
                      type="button"
                      size="sm"
                      variant="secondary"
                      disabled={inviting}
                      onClick={() => handleInvite(user)}
                    >
                      {inviting ? <Loader2 className="h-4 w-4 animate-spin" /> : "Invite"}
                    </Button>
                  </li>
                );
              })}
            </ul>
          ) : (
            !isFetching && <p className="px-1 py-3 text-sm text-muted-foreground">No users found.</p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
