"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";

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
import { useCreateRoom, useLookupUser } from "@/hooks/useRooms";
import { ApiError } from "@/lib/api";
import { isValidUuid, validateRoomName } from "@/lib/validation";

type RoomKind = "channel" | "direct";

export function CreateRoomDialog() {
  const router = useRouter();
  const { user } = useAuth();
  const createRoom = useCreateRoom();

  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<RoomKind>("channel");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [peerUserId, setPeerUserId] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const isSelf = !!user && peerUserId.toLowerCase() === user.id.toLowerCase();
  const peerLookup = useLookupUser(isSelf ? "" : peerUserId);

  function reset() {
    setKind("channel");
    setName("");
    setDescription("");
    setPeerUserId("");
    setNameError(null);
    setFormError(null);
  }

  function handleOpenChange(next: boolean) {
    setOpen(next);
    if (!next) reset();
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
      } else {
        if (!peerLookup.data) return; // submit is disabled in this state anyway

        const room = await createRoom.mutateAsync({
          type: "direct",
          peer_user_id: peerUserId,
        });
        handleOpenChange(false);
        router.push(`/chat/${room.id}`);
      }
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Something went wrong. Please try again.");
    }
  }

  let peerStatus: { text: string; className: string } | null = null;
  if (isSelf) {
    peerStatus = { text: "That's your own user ID", className: "text-destructive" };
  } else if (peerUserId && !isValidUuid(peerUserId)) {
    peerStatus = { text: "Enter a valid user ID", className: "text-muted-foreground" };
  } else if (peerLookup.isLoading) {
    peerStatus = { text: "Looking up...", className: "text-muted-foreground" };
  } else if (peerLookup.isSuccess && peerLookup.data) {
    peerStatus = { text: `✓ Found: @${peerLookup.data.username}`, className: "text-primary" };
  } else if (peerLookup.isError) {
    const notFound = peerLookup.error instanceof ApiError && peerLookup.error.code === "not_found";
    peerStatus = {
      text: notFound ? "No user found with that ID" : "Couldn't look up that user",
      className: "text-destructive",
    };
  }

  const canSubmitDirect = isValidUuid(peerUserId) && !isSelf && peerLookup.isSuccess;
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
            <DialogDescription>Start a channel or message someone directly.</DialogDescription>
          </DialogHeader>

          <div className="flex gap-2 py-4">
            <Button
              type="button"
              variant={kind === "channel" ? "default" : "outline"}
              className="flex-1"
              onClick={() => setKind("channel")}
            >
              Channel
            </Button>
            <Button
              type="button"
              variant={kind === "direct" ? "default" : "outline"}
              className="flex-1"
              onClick={() => setKind("direct")}
            >
              Direct Message
            </Button>
          </div>

          {formError && (
            <div className="mb-4 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {formError}
            </div>
          )}

          {kind === "channel" ? (
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
          ) : (
            <div className="space-y-1.5">
              <Label htmlFor="peer-user-id">Peer user ID</Label>
              <Input
                id="peer-user-id"
                placeholder="00000000-0000-0000-0000-000000000000"
                value={peerUserId}
                onChange={(e) => setPeerUserId(e.target.value.trim())}
              />
              {peerStatus && <p className={`text-sm ${peerStatus.className}`}>{peerStatus.text}</p>}
            </div>
          )}

          <DialogFooter className="mt-6">
            <Button type="button" variant="outline" onClick={() => handleOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={isPending || (kind === "direct" && !canSubmitDirect)}>
              {isPending ? "Creating..." : kind === "channel" ? "Create channel" : "Start DM"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
