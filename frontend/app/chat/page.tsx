import { MessageSquare } from "lucide-react";

import { MobileSidebarTrigger } from "@/components/MobileSidebarTrigger";

// Empty state for /chat itself — a real room fills the same slot at
// /chat/[roomId] (see app/chat/[roomId]/page.tsx). Below md there's no
// static sidebar column, so this is one of the two screens (the other is
// RoomHeader) that surfaces a way back into the rooms drawer.
export default function ChatEmptyStatePage() {
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-1 border-b px-3 py-4 md:hidden">
        <MobileSidebarTrigger />
        <span className="text-sm font-medium">ConvoyChat</span>
      </div>
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
        <MessageSquare className="h-10 w-10 text-muted-foreground" />
        <h1 className="text-lg font-medium">Pick a room to get started</h1>
        <p className="max-w-sm text-sm text-muted-foreground">
          Select a channel or direct message from the sidebar, or create a new one.
        </p>
      </div>
    </div>
  );
}
