import type { NextConfig } from "next";

const config: NextConfig = {
  poweredByHeader: false,
  // Operators upload payers' code lists through a server action; the API
  // takes up to 16 MiB.
  experimental: { serverActions: { bodySizeLimit: "16mb" } },
};

export default config;
