import type { MetadataRoute } from "next";

import { site } from "./site";

export default function robots(): MetadataRoute.Robots {
  // Flip to allow (and set robots.index in layout.tsx) once the name is final.
  // The console is never indexed.
  return { rules: { userAgent: "*", disallow: "/" }, sitemap: `${site.url}/sitemap.xml` };
}
