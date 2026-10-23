import type { Metadata } from "next";
import Link from "next/link";

import { Gallery } from "./gallery";

export const metadata: Metadata = {
  title: "Form kit",
  description: "The standard form fields used across Payahub.",
  robots: { index: false, follow: false },
};

export default function UIPage() {
  return (
    <div className="ph-gallery">
      <header className="ph-gallery__head">
        <Link href="/" className="ph-gallery__brand">
          <img src="/brand/payahub-symbol-small-color.svg" alt="" width={28} height={28} />
          Payahub
        </Link>
        <div>
          <h1>Form kit</h1>
          <p>
            Every form on the site builds from these fields, imported from <code>@/components/ui</code>. Fill any of
            them and press <strong>Show what the form sends</strong> to see the exact FormData a server action receives.
          </p>
        </div>
      </header>
      <Gallery />
    </div>
  );
}
