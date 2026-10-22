import { dismissKey } from "@/app/actions";
import { readFlashKey } from "@/lib/flash";

import { CopyButton } from "./copy";

// KeyBanner shows a key made moments ago, once. It is gone after dismissal
// or within the hour; the console never shows a full key again.
export async function KeyBanner() {
  const flash = await readFlashKey();
  if (!flash) return null;
  return (
    <section className="app-notice" aria-labelledby="new-key-h">
      <div>
        <h2 id="new-key-h" className="app-card__title">
          Your new {flash.env} key
        </h2>
        <p className="app-muted">Copy it now and keep it somewhere safe. We store only its fingerprint, so we cannot show it again.</p>
      </div>
      <div className="app-keyline">
        <code className="app-code">{flash.key}</code>
        <CopyButton value={flash.key} />
      </div>
      <form action={dismissKey}>
        <button type="submit" className="app-button app-button--quiet">
          I have copied it
        </button>
      </form>
    </section>
  );
}
