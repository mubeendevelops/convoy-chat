"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { AdminRoomMembersSheet } from "@/components/AdminRoomMembersSheet";
import { STATUS_COLOR, STATUS_LABEL } from "@/components/UserPresence";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminPresence, useAdminRooms } from "@/hooks/useAdmin";
import { useRequireSystemAdmin } from "@/hooks/useAuth";
import { roomTypeLabel } from "@/lib/rooms";
import { cn } from "@/lib/utils";

// System-admin dashboard (Phase 3, post-v1) — a single page, not a multi-tab
// nav, given the modest scope plan.md's proposal settled on: every room in
// the system (regardless of the caller's own membership, with a read-only
// "View members" drill-in) and a system-wide presence snapshot. "Message
// moderation" needs no UI of its own here — it's the existing per-message
// delete affordance in MessageBubble, now also reachable by a system admin
// from inside any room (see CLAUDE.md's admin-dashboard entry). Not nested
// under app/chat/'s two-pane shell — this is a different UI concern with no
// rooms sidebar or composer.
export default function AdminPage() {
  const { isReady, isHydrated } = useRequireSystemAdmin();
  const rooms = useAdminRooms();
  const presence = useAdminPresence();
  const [viewingRoomId, setViewingRoomId] = useState<string | undefined>(undefined);

  if (!isHydrated || !isReady) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background p-8">
        <Skeleton className="h-8 w-48" />
      </div>
    );
  }

  // Online-first sort — the more actionable rows (who's around right now)
  // read better above the long tail of offline accounts.
  const statusOrder = { online: 0, away: 1, offline: 2 } as const;
  const sortedPresence = [...(presence.data ?? [])].sort(
    (a, b) => statusOrder[a.status] - statusOrder[b.status] || a.username.localeCompare(b.username),
  );

  return (
    <main className="min-h-screen bg-background p-6 text-foreground md:p-10">
      <div className="mx-auto max-w-5xl space-y-8">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold">Admin dashboard</h1>
            <p className="text-sm text-muted-foreground">
              System-wide visibility — every room and every user&apos;s presence, regardless of your own
              membership.
            </p>
          </div>
          <Button variant="outline" size="sm" className="gap-2" asChild>
            <Link href="/chat">
              <ArrowLeft className="h-4 w-4" />
              Back to chat
            </Link>
          </Button>
        </div>

        <section className="space-y-3">
          <h2 className="text-lg font-medium">Rooms</h2>
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b bg-muted/50 text-xs font-semibold uppercase text-muted-foreground">
                  <th className="px-4 py-2">Name</th>
                  <th className="px-4 py-2">Type</th>
                  <th className="px-4 py-2">Creator</th>
                  <th className="px-4 py-2">Members</th>
                  <th className="px-4 py-2">Created</th>
                  <th className="px-4 py-2" />
                </tr>
              </thead>
              <tbody>
                {rooms.isLoading && (
                  <tr>
                    <td colSpan={6} className="px-4 py-4">
                      <Skeleton className="h-6 w-full" />
                    </td>
                  </tr>
                )}
                {rooms.isError && (
                  <tr>
                    <td colSpan={6} className="px-4 py-4 text-destructive">
                      Couldn&apos;t load rooms.
                    </td>
                  </tr>
                )}
                {rooms.data?.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-4 text-muted-foreground">
                      No rooms yet.
                    </td>
                  </tr>
                )}
                {rooms.data?.map((room) => (
                  <tr key={room.id} className="border-b last:border-0">
                    <td className="px-4 py-2 font-medium">{room.name ?? "—"}</td>
                    <td className="px-4 py-2">{roomTypeLabel(room.type)}</td>
                    <td className="px-4 py-2">{room.creator.username}</td>
                    <td className="px-4 py-2">{room.member_count}</td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {new Date(room.created_at).toLocaleDateString()}
                    </td>
                    <td className="px-4 py-2">
                      <Button variant="ghost" size="sm" onClick={() => setViewingRoomId(room.id)}>
                        View members
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className="space-y-3">
          <h2 className="text-lg font-medium">Presence</h2>
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b bg-muted/50 text-xs font-semibold uppercase text-muted-foreground">
                  <th className="px-4 py-2">User</th>
                  <th className="px-4 py-2">Status</th>
                  <th className="px-4 py-2">Last seen</th>
                </tr>
              </thead>
              <tbody>
                {presence.isLoading && (
                  <tr>
                    <td colSpan={3} className="px-4 py-4">
                      <Skeleton className="h-6 w-full" />
                    </td>
                  </tr>
                )}
                {presence.isError && (
                  <tr>
                    <td colSpan={3} className="px-4 py-4 text-destructive">
                      Couldn&apos;t load presence.
                    </td>
                  </tr>
                )}
                {sortedPresence.map((entry) => (
                  <tr key={entry.user_id} className="border-b last:border-0">
                    <td className="px-4 py-2 font-medium">{entry.username}</td>
                    <td className="px-4 py-2">
                      <span className="flex items-center gap-2">
                        <span className={cn("h-2 w-2 rounded-full", STATUS_COLOR[entry.status])} />
                        {STATUS_LABEL[entry.status]}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {entry.last_seen_at ? new Date(entry.last_seen_at).toLocaleString() : "Never"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <AdminRoomMembersSheet
        roomId={viewingRoomId}
        open={!!viewingRoomId}
        onOpenChange={(open) => !open && setViewingRoomId(undefined)}
      />
    </main>
  );
}
