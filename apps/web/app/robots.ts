import type { MetadataRoute } from "next";

import { site } from "./site";

export default function robots(): MetadataRoute.Robots {
  // Flip to allow once the landing page and name are final.
  return { rules: { userAgent: "*", disallow: "/" }, sitemap: `${site.url}/sitemap.xml` };
}
