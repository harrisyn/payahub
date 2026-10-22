import { site } from "@/app/site";

// The Ananse exchange lockup: the spider and the outlined "payahub" wordmark.
// Colour on light grounds, reversed on Night. Drawn at 903x256.
export function Logo({ tone = "color", className = "logo" }: { tone?: "color" | "reversed"; className?: string }) {
  return (
    <img
      src={`/brand/payahub-logo-horizontal-${tone}.svg`}
      alt={site.name}
      width={903}
      height={256}
      className={className}
    />
  );
}
