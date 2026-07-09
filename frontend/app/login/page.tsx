"use client";

import { useState, type FormEvent } from "react";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuth, useRequireGuest } from "@/hooks/useAuth";
import { ApiError } from "@/lib/api";
import { validateEmail } from "@/lib/validation";

interface FieldErrors {
  email?: string;
  password?: string;
}

export default function LoginPage() {
  const { isReady, isHydrated } = useRequireGuest();
  const { login, isLoggingIn } = useAuth();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);

  if (!isHydrated || !isReady) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background p-8">
        <Skeleton className="h-8 w-48" />
      </div>
    );
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setFormError(null);

    const errors: FieldErrors = {
      email: validateEmail(email) ?? undefined,
      password: password ? undefined : "Password is required",
    };
    setFieldErrors(errors);
    if (errors.email || errors.password) return;

    try {
      await login({ email, password });
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Something went wrong. Please try again.");
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-8 text-foreground">
      <div className="w-full max-w-sm space-y-6">
        <div className="space-y-1 text-center">
          <h1 className="text-2xl font-semibold">Log in to ConvoyChat</h1>
          <p className="text-sm text-muted-foreground">Welcome back.</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4" noValidate>
          {formError && (
            <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {formError}
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
            {fieldErrors.email && <p className="text-sm text-destructive">{fieldErrors.email}</p>}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            {fieldErrors.password && <p className="text-sm text-destructive">{fieldErrors.password}</p>}
          </div>

          <Button type="submit" className="w-full" disabled={isLoggingIn}>
            {isLoggingIn ? "Logging in..." : "Log in"}
          </Button>
        </form>

        <p className="text-center text-sm text-muted-foreground">
          Don&apos;t have an account?{" "}
          <Link href="/signup" className="text-primary underline-offset-4 hover:underline">
            Sign up
          </Link>
        </p>
      </div>
    </main>
  );
}
