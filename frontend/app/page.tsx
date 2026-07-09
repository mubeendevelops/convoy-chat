"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { Skeleton } from "@/components/ui/skeleton";
import { useAuth } from "@/hooks/useAuth";

export default function Home() {
  const { isAuthenticated, isHydrated } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!isHydrated) return;
    router.replace(isAuthenticated ? "/chat" : "/login");
  }, [isHydrated, isAuthenticated, router]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-8">
      <Skeleton className="h-8 w-48" />
    </div>
  );
}
