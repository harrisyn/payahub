import type { MetadataRoute } from "next";

import { site } from "./site";

export default function robots(): MetadataRoute.Robots {
  // The console, the type-ahead route and the form kit page are never indexed.
  const private_ = ["/console", "/lookup", "/ui"];
  return {
    rules: site.indexable ? { userAgent: "*", allow: "/", disallow: private_ } : { userAgent: "*", disallow: "/" },
    sitemap: `${site.url}/sitemap.xml`,
  };
}
