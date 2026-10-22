"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const items = [
  { href: "/console", label: "Overview" },
  { href: "/console/setup", label: "How you bill" },
  { href: "/console/insurers", label: "Insurers" },
  { href: "/console/claims", label: "Claims" },
  { href: "/console/developers", label: "Developers" },
  { href: "/console/plan", label: "Plan and usage" },
];

export function ConsoleNav() {
  const path = usePathname();
  return (
    <nav aria-label="Console" className="app-nav">
      {items.map((i) => {
        const active = i.href === "/console" ? path === i.href : path.startsWith(i.href);
        return (
          <Link key={i.href} href={i.href} className="app-nav__item" aria-current={active ? "page" : undefined}>
            {i.label}
          </Link>
        );
      })}
    </nav>
  );
}
