"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Compass, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { toast } from "@/hooks/use-toast";
import { useBrowseChannels, useJoinChannel } from "@/hooks/useRooms";
import { ApiError } from "@/lib/api";
import type { PublicChannel } from "@/lib/types";

// Self-serve counterpart to InviteMemberDialog's admin-only invite flow:
// lists public channels the caller isn't in yet, with a per-row Join button.
// The query only runs while the dialog is open (enabled: open) — no point
// fetching a list nobody can see yet, same "don't fetch what isn't visible"
// spirit as InviteMemberDialog's debounced search. No search box: a plain
// list is fine at v1 scale, same call already made for RoomsList's
// direct-room N+1 lookups.
export function BrowseChannelsDialog() {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const { data: channels, isLoading, isError } = useBrowseChannels(open);
  const join = useJoinChannel();

  async function handleJoin(channel: PublicChannel) {
    try {
      const member = await join.mutateAsync(channel.id);
      setOpen(false);
      router.push(`/chat/${member.room_id}`);
    } catch (err) {
      toast({
        variant: "destructive",
        title: "Couldn't join that channel",
        description: err instanceof ApiError ? err.message : "Check your connection and try again.",
      });
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" className="w-full gap-2">
          <Compass className="h-4 w-4" />
          Browse channels
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Browse channels</DialogTitle>
          <DialogDescription>Join a public channel you&apos;re not already in.</DialogDescription>
        </DialogHeader>

        <div className="mt-2 max-h-80 min-h-[3rem] overflow-y-auto" aria-live="polite">
          {isLoading ? (
            <p className="px-1 py-3 text-sm text-muted-foreground">Loading…</p>
          ) : isError ? (
            <p className="px-1 py-3 text-sm text-destructive">Couldn&apos;t load public channels. Try again.</p>
          ) : channels && channels.length > 0 ? (
            <ul className="space-y-1">
              {channels.map((channel) => {
                const joining = join.isPending && join.variables === channel.id;
                return (
                  <li key={channel.id} className="flex items-center gap-3 rounded-md px-1 py-1.5">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{channel.name ?? "Untitled channel"}</p>
                      <p className="truncate text-xs text-muted-foreground">
                        {channel.member_count} {channel.member_count === 1 ? "member" : "members"}
                        {channel.description ? ` · ${channel.description}` : ""}
                      </p>
                    </div>
                    <Button
                      type="button"
                      size="sm"
                      variant="secondary"
                      disabled={joining}
                      onClick={() => handleJoin(channel)}
                    >
                      {joining ? <Loader2 className="h-4 w-4 animate-spin" /> : "Join"}
                    </Button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="px-1 py-3 text-sm text-muted-foreground">No public channels to join right now.</p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
