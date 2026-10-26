import { NextResponse, type NextRequest } from "next/server";

// The site and the app share one server but live on two hosts: the landing
// page and docs on the main domain, the console (operators' super admin
// included, under /console/operator) on PAYAHUB_APP_URL, e.g.
// https://app.payahub.com. Sign-in and sign-up move to the app host so the
// session cookie is set there; the app host's root opens the console.
// Without PAYAHUB_APP_URL (local development) everything is on one host.
const APP_PATHS = ["/console", "/signin", "/start", "/join"];

export function proxy(req: NextRequest) {
  const raw = process.env.PAYAHUB_APP_URL;
  if (!raw) return NextResponse.next();
  const app = new URL(raw);
  const host = (req.headers.get("x-forwarded-host") ?? req.headers.get("host") ?? "").split(",")[0].trim().toLowerCase();
  const { pathname, search } = req.nextUrl;
  const onApp = host === app.host;
  const appPath = APP_PATHS.some((p) => pathname === p || pathname.startsWith(p + "/"));

  if (onApp && pathname === "/") {
    return NextResponse.redirect(new URL("/console" + search, app), 308);
  }
  if (!onApp && appPath) {
    return NextResponse.redirect(new URL(pathname + search, app), 308);
  }
  return NextResponse.next();
}

export const config = {
  matcher: ["/", "/console/:path*", "/signin", "/start", "/join/:path*"],
};
