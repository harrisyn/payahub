import type { Perm, Role } from "./types";

export const roles: { id: Role; name: string; does: string }[] = [
  {
    id: "owner",
    name: "Owner",
    does: "Everything, including adding and removing owners.",
  },
  {
    id: "admin",
    name: "Admin",
    does: "Everything except owners: claims, setup, billing, keys and the team.",
  },
  {
    id: "claims",
    name: "Claims",
    does: "Creates, checks and sends claims, and reads claim forms.",
  },
  {
    id: "finance",
    name: "Finance",
    does: "Funds the wallet, asks for withdrawals and changes the plan.",
  },
  {
    id: "developer",
    name: "Developer",
    does: "API keys, webhooks, insurer accounts, the price list and code mappings.",
  },
  {
    id: "viewer",
    name: "Viewer",
    does: "Sees everything and changes nothing.",
  },
];

export const roleName = Object.fromEntries(
  roles.map((r) => [r.id, r.name]),
) as Record<Role, string>;

export const can = (perms: Perm[] | undefined, p: Perm) => !!perms?.includes(p);
