import { MessageSquare } from "lucide-react";

// Empty state for /chat itself — a real room fills the same slot at
// /chat/[roomId] (see app/chat/[roomId]/page.tsx).
export default function ChatEmptyStatePage() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
      <MessageSquare className="h-10 w-10 text-muted-foreground" />
      <h1 className="text-lg font-medium">Pick a room to get started</h1>
      <p className="max-w-sm text-sm text-muted-foreground">
        Select a channel or direct message from the sidebar, or create a new one.
      </p>
    </div>
  );
}
