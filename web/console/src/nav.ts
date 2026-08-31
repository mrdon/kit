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
  // app is the registry name of the feature app this section belongs to.
  // When that app is disabled for the workspace, the section is hidden.
  // Omit for core surfaces (Skills, Jobs, Roles, Apps) that are always shown.
  app?: string;
}

export const SECTIONS: Section[] = [
  {
    to: '/tasks',
    label: 'Tasks',
    blurb: 'List and board for everything your roles own.',
    app: 'task',
  },
  {
    to: '/events',
    label: 'Events',
    blurb: 'Author events once; they sync to the calendar and the website.',
    app: 'events',
  },
  {
    to: '/expenses',
    label: 'Expenses',
    blurb: 'File and approve expense reports with receipts.',
    app: 'expense',
  },
  {
    to: '/menu',
    label: 'Menu',
    blurb: 'Published tap lists, and the address to put one on a screen.',
    app: 'menu',
  },
  {
    to: '/kiosk',
    label: 'Kiosk',
    blurb: 'Change what your wall screens show without touching the machine.',
    app: 'kiosk',
  },
  {
    to: '/vault',
    label: 'Vault',
    blurb: 'Shared team secrets, end-to-end encrypted in your browser.',
    app: 'vault',
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
    to: '/connect',
    label: 'Connect AI tools',
    blurb: 'Your MCP endpoint for Claude Code, Cursor, and other AI clients.',
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
    app: 'expense',
  },
  {
    to: '/admin/apps',
    label: 'Apps',
    blurb: 'Turn features like the vault, expenses, or voting on or off.',
    admin: true,
  },
  {
    // Not admin-gated: user-scoped connectors (personal email) are
    // self-service. Workspace-wide connectors are marked admin-only on the
    // page itself and their Connect button 403s for non-admins.
    to: '/admin/integrations',
    label: 'Integrations',
    blurb: 'Connect email, Square, Google Calendar, and other services.',
    app: 'integrations',
  },
  {
    to: '/admin/netlify',
    label: 'Website',
    blurb: 'Connect Netlify + GitHub so the team can request site changes.',
    admin: true,
    app: 'netlify',
  },
  {
    to: '/admin/events',
    label: 'Events calendar & feed',
    blurb: 'Pick the calendar events sync to, and the feed your website builds from.',
    admin: true,
    app: 'events',
  },
  {
    to: '/admin/events-staff',
    label: 'Daily event notices',
    blurb: 'Pick the Slack channel that gets the morning post, and who it mentions.',
    admin: true,
    app: 'events',
  },
  {
    to: '/admin/square-shifts',
    label: 'Square Shift Sync',
    blurb: 'Mirror your published Square schedule into a Google Calendar.',
    admin: true,
    app: 'square_shifts',
  },
  {
    to: '/admin/widget',
    label: 'Chat widget',
    blurb: 'Mint and revoke embed tokens for the website chat widget.',
    admin: true,
    app: 'widget',
  },
];

// visibleSections drops sections whose owning app is disabled for the
// workspace. Core sections (no `app`) always pass. The caller passes the
// `disabled_apps` list from /me.
export function visibleSections(
  sections: Section[],
  disabledApps: string[] | undefined,
): Section[] {
  const disabled = new Set(disabledApps ?? []);
  return sections.filter((s) => !s.app || !disabled.has(s.app));
}

// The top nav and home launcher show only the primary sections; the
// admin-only ones (roles, integrations, …) are infrequent setup, so they're
// grouped behind the Admin area (pages/Admin.tsx) to keep the chrome clean.
export const PRIMARY_SECTIONS = SECTIONS.filter((s) => !s.admin);
export const ADMIN_SECTIONS = SECTIONS.filter((s) => s.admin);
