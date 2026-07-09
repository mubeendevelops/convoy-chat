"use client";

import { useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { Send } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { validateMessageContent } from "@/lib/validation";

const MAX_TEXTAREA_HEIGHT_PX = 160;

export function MessageInput({ onSend }: { onSend: (content: string) => void }) {
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
  const canSend = !validateMessageContent(trimmed);

  function handleSend() {
    if (!canSend) return;
    // Sending is optimistic and instant (WS, or REST fallback) — clear the box
    // right away and fire. Success/failure surfaces inline on the bubble
    // itself, so there's no pending state to block on here.
    setContent("");
    onSend(trimmed);
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
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
          rows={1}
          className="min-h-[44px] resize-none overflow-y-auto"
        />
        <Button size="icon" onClick={handleSend} disabled={!canSend} aria-label="Send message">
          <Send className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}
