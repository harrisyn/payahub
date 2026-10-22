import type { Metadata, Viewport } from "next";
import { IBM_Plex_Mono, Schibsted_Grotesk } from "next/font/google";
import type { ReactNode } from "react";

import "./globals.css";
import { site } from "./site";

const grotesk = Schibsted_Grotesk({
  subsets: ["latin"],
  weight: ["400", "500", "700", "800"],
  variable: "--font-grotesk",
  display: "swap",
});

// One job: IDs, keys and figures in console tables, where digits must line up.
const mono = IBM_Plex_Mono({ subsets: ["latin"], weight: ["400", "500"], variable: "--font-mono", display: "swap" });

export const metadata: Metadata = {
  metadataBase: new URL(site.url),
  title: { default: site.name, template: `%s · ${site.name}` },
  description: site.description,
  applicationName: site.name,
  openGraph: { siteName: site.name, type: "website", locale: "en_GH", title: site.name, description: site.description },
  twitter: { card: "summary_large_image", title: site.name, description: site.description },
  // Indexing stays off until the product name is final; flip in robots.ts too.
  robots: { index: false, follow: false },
};

export const viewport: Viewport = { themeColor: "#1F0F3A" };

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en-GH" className={`${grotesk.variable} ${mono.variable}`}>
      <body>{children}</body>
    </html>
  );
}
