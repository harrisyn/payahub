// Single source for the name, URL and contact, since the product name is still open.
export const site = {
  name: "Payahub",
  url: process.env.NEXT_PUBLIC_SITE_URL ?? "https://payahub.example",
  // Sample contact address; replace before launch.
  email: process.env.NEXT_PUBLIC_CONTACT_EMAIL ?? "hello@payahub.example",
  city: "Accra",
  description:
    "Payahub is one API that sends your clinic's insurance claims to every insurer in Ghana, checks each one before it leaves, and never loses one on the way.",
};
