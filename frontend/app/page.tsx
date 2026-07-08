import { ThemeToggle } from "@/components/theme-toggle";

export default function Home() {
  return (
    <main className="relative flex min-h-screen flex-col items-center justify-center gap-8 bg-background p-8 text-foreground">
      <div className="absolute right-6 top-6">
        <ThemeToggle />
      </div>

      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-4xl font-bold tracking-tight">ConvoyChat</h1>
        <p className="text-muted-foreground">
          Real-time team chat — channels, DMs, presence, and more.
        </p>
      </div>

      <div className="flex w-full max-w-sm flex-col gap-2 rounded-lg border bg-card p-4 shadow-sm">
        <div className="ml-auto max-w-[75%] rounded-lg bg-bubble-outgoing px-3 py-2 text-sm text-bubble-outgoing-foreground">
          Hey team, standup in 5?
        </div>
        <div className="mr-auto max-w-[75%] rounded-lg bg-bubble-incoming px-3 py-2 text-sm text-bubble-incoming-foreground">
          On my way 👍
        </div>
      </div>
    </main>
  );
}
