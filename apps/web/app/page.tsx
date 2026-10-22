import { site } from "./site";

// Placeholder only. The real landing page is designed with the
// design-taste-frontend skill (see .claude/skills) once the name is final;
// indexing stays off until then.
export default function Home() {
  return (
    <main>
      <h1>{site.name}</h1>
      <p>{site.tagline}</p>
    </main>
  );
}
