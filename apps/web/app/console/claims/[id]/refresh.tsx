"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

// Refresh reloads the claim while it is still on its way to the insurer.
export function Refresh() {
  const router = useRouter();
  useEffect(() => {
    const t = setInterval(() => router.refresh(), 1500);
    return () => clearInterval(t);
  }, [router]);
  return null;
}
