// Section is one navigable area of the console. The same list drives the
// top-nav links and the launcher tiles, so they never drift. `admin`
// sections are hidden from non-admins client-side (cosmetic — the APIs
// enforce admin access server-side regardless).
//
// Phase 1 has a single section; Tasks and Vault join here as later phases
// land. This stays a static list until app-contributed sections actually
// exist (then it generalizes into a /web/api/nav registry).
export interface Section {
  to: string;
  label: string;
  blurb: string;
  admin?: boolean;
}

export const SECTIONS: Section[] = [
  {
    to: '/tasks',
    label: 'Tasks',
    blurb: 'List and board for everything your roles own.',
  },
  {
    to: '/integrations',
    label: 'Integrations',
    blurb: 'Connect Netlify, GitHub, and other services.',
    admin: true,
  },
  {
    to: '/netlify',
    label: 'Website',
    blurb: 'Connect Netlify + GitHub so the team can request site changes.',
    admin: true,
  },
  {
    to: '/widget',
    label: 'Chat widget',
    blurb: 'Mint and revoke embed tokens for the website chat widget.',
    admin: true,
  },
];
