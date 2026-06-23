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
    to: '/expenses',
    label: 'Expenses',
    blurb: 'File and approve expense reports with receipts.',
  },
  {
    to: '/vault',
    label: 'Vault',
    blurb: 'Shared team secrets, end-to-end encrypted in your browser.',
  },
  {
    to: '/skills',
    label: 'Skills',
    blurb: 'Browse the knowledge base; admins create and edit articles.',
  },
  {
    to: '/jobs',
    label: 'Jobs',
    blurb: 'Scheduled work — review, edit, and remove your jobs.',
  },
  {
    to: '/admin/roles',
    label: 'Roles',
    blurb: 'See who belongs to which role and change membership.',
    admin: true,
  },
  {
    to: '/admin/expenses',
    label: 'Expense settings',
    blurb: 'Choose which role approves expense reports.',
    admin: true,
  },
  {
    to: '/admin/integrations',
    label: 'Integrations',
    blurb: 'Connect Netlify, GitHub, and other services.',
    admin: true,
  },
  {
    to: '/admin/netlify',
    label: 'Website',
    blurb: 'Connect Netlify + GitHub so the team can request site changes.',
    admin: true,
  },
  {
    to: '/admin/widget',
    label: 'Chat widget',
    blurb: 'Mint and revoke embed tokens for the website chat widget.',
    admin: true,
  },
];

// The top nav and home launcher show only the primary sections; the
// admin-only ones (roles, integrations, …) are infrequent setup, so they're
// grouped behind the Admin area (pages/Admin.tsx) to keep the chrome clean.
export const PRIMARY_SECTIONS = SECTIONS.filter((s) => !s.admin);
export const ADMIN_SECTIONS = SECTIONS.filter((s) => s.admin);
