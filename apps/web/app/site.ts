// Single source for the name, URL and contact, since the product name is still open.
export const site = {
  name: "Payahub",
  url: process.env.NEXT_PUBLIC_SITE_URL ?? "https://payahub.example",
  // Sample contact address; replace before launch.
  email: process.env.NEXT_PUBLIC_CONTACT_EMAIL ?? "hello@payahub.example",
  city: "Accra",
  // Search engines stay out until launch; set NEXT_PUBLIC_SITE_INDEXABLE=1 to let them in.
  indexable: process.env.NEXT_PUBLIC_SITE_INDEXABLE === "1",
  description:
    "Payahub sends your clinic's health insurance claims to every insurer in Ghana through one API and console. Each claim is coded, checked and tracked to acknowledgement, whichever platform the insurer uses.",
};
