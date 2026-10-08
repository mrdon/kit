import { StackViews, type StackView } from '../types';

// Where each stack view lives in the router. The feed keeps the root so
// existing Slack deep-links (/#source:kind:id) still land on it.
export const VIEW_PATHS: Record<StackView, string> = {
  [StackViews.feed]: '/',
  [StackViews.tasks]: '/tasks',
};

export const VIEW_LABELS: Record<StackView, string> = {
  [StackViews.feed]: 'For you',
  [StackViews.tasks]: 'All tasks',
};

// LAST_VIEW_STORAGE remembers which view the user was swiping in, so the
// detail page's back, swipe-away and post-action returns land on that view
// rather than always on the feed.
const LAST_VIEW_STORAGE = 'kit:stack:view';

export function rememberView(view: StackView) {
  try {
    sessionStorage.setItem(LAST_VIEW_STORAGE, view);
  } catch {
    // sessionStorage can throw in private-mode Safari; returning to the
    // feed instead is the harmless fallback.
  }
}

export function lastViewPath(): string {
  let saved: string | null = null;
  try {
    saved = sessionStorage.getItem(LAST_VIEW_STORAGE);
  } catch {
    // see rememberView
  }
  return saved === StackViews.tasks ? VIEW_PATHS[StackViews.tasks] : VIEW_PATHS[StackViews.feed];
}

// topKeyStorage is the sessionStorage key for the card at the top of the
// viewport, per view, so switching views does not drag the scroll position
// across. The feed keeps the original key.
export function topKeyStorage(view: StackView): string {
  return view === StackViews.feed ? 'kit:stack:topKey' : `kit:stack:topKey:${view}`;
}
