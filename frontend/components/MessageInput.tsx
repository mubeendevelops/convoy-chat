"use client";

import { useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { Send } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { useSendMessage } from "@/hooks/useMessages";
import { validateMessageContent } from "@/lib/validation";

const MAX_TEXTAREA_HEIGHT_PX = 160;

export function MessageInput({ sendMessage }: { sendMessage: ReturnType<typeof useSendMessage> }) {
  const [content, setContent] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Auto-grow with typed content (up to a cap, then it scrolls internally)
  // so a Shift+Enter multi-line message doesn't feel cramped.
  useLayoutEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_TEXTAREA_HEIGHT_PX)}px`;
  }, [content]);

  const trimmed = content.trim();
  const canSend = !validateMessageContent(trimmed) && !sendMessage.isPending;

  async function handleSend() {
    if (!canSend) return;

    setContent("");
    try {
      await sendMessage.mutateAsync({ content: trimmed, clientId: crypto.randomUUID() });
    } catch {
      // Surfaced inline on the message itself — the mutation's onError
      // marks that optimistic bubble "failed" (see MessageBubble), so
      // there's nothing further to show here.
    }
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void handleSend();
    }
  }

  return (
    <div className="border-t p-4">
      <div className="flex items-end gap-2">
        <Textarea
          ref={textareaRef}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Message..."
          disabled={sendMessage.isPending}
          rows={1}
          className="min-h-[44px] resize-none overflow-y-auto"
        />
        <Button size="icon" onClick={() => void handleSend()} disabled={!canSend} aria-label="Send message">
          <Send className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}
