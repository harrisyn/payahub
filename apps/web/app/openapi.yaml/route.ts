import { API_URL } from "@/lib/api";

// The public interface spec, served from the API so it always matches it.
export async function GET() {
  const res = await fetch(`${API_URL}/v1/openapi.yaml`, { next: { revalidate: 300 } }).catch(() => null);
  if (!res?.ok) return new Response("The spec is unavailable right now.", { status: 502 });
  return new Response(await res.text(), {
    headers: { "Content-Type": "application/yaml; charset=utf-8", "Content-Disposition": 'inline; filename="payahub.v1.yaml"' },
  });
}
