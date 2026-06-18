// Shared display helpers for the expenses console pages.

export const STATUSES = [
  'draft',
  'submitted',
  'approved',
  'rejected',
  'reimbursed',
] as const;

export const STATUS_LABEL: Record<string, string> = {
  draft: 'Draft',
  submitted: 'Submitted',
  approved: 'Approved',
  rejected: 'Rejected',
  reimbursed: 'Reimbursed',
};

// formatCents renders a minor-unit amount in its currency, mirroring the
// server's format.go so the two never disagree.
export function formatCents(cents: number, currency = 'USD'): string {
  const amount = (cents / 100).toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  const symbol: Record<string, string> = {
    USD: '$',
    CAD: '$',
    AUD: '$',
    NZD: '$',
    GBP: '£',
    EUR: '€',
  };
  const s = symbol[currency];
  return s ? `${s}${amount}` : `${amount} ${currency}`;
}
