"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const items = [
  { href: "/console", label: "Overview" },
  { href: "/console/setup", label: "How you bill" },
  { href: "/console/insurers", label: "Insurers" },
  { href: "/console/prices", label: "Price list" },
  { href: "/console/items", label: "Services and medicines" },
  { href: "/console/claims", label: "Claims" },
  { href: "/console/developers", label: "Developers" },
  { href: "/console/plan", label: "Plan and usage" },
  { href: "/console/wallet", label: "Wallet" },
];

const operatorItems = [
  { href: "/console/operator", label: "Catalogue" },
  { href: "/console/operator/withdrawals", label: "Withdrawals" },
  { href: "/console/operator/ai", label: "AI models" },
];

export function ConsoleNav({ operator = false }: { operator?: boolean }) {
  const path = usePathname();
  const shown = operator ? [...items, ...operatorItems] : items;
  // The longest matching link is the current page, so a page under the
  // catalogue does not light up two links.
  const current = shown
    .filter((i) =>
      i.href === "/console"
        ? path === i.href
        : path === i.href || path.startsWith(i.href + "/"),
    )
    .sort((a, b) => b.href.length - a.href.length)[0]?.href;
  return (
    <nav aria-label="Console" className="app-nav">
      {shown.map((i) => {
        const active = i.href === current;
        return (
          <Link
            key={i.href}
            href={i.href}
            className="app-nav__item"
            aria-current={active ? "page" : undefined}
          >
            {i.label}
          </Link>
        );
      })}
    </nav>
  );
}
