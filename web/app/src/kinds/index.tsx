import type { ComponentType } from 'react';
import type { StackItem } from '../types';
import { cardsDecision } from './cards_decision';
import { cardsBriefing } from './cards_briefing';
import { todoTodo } from './todo_todo';

// A KindRenderer is a pair of components: one small strip of chrome shown
// on the card face (below the title/body) and one block shown in the
// detail view. Each is keyed by "source_app:kind" so two apps can share
// the word "todo" without colliding.
export type KindRenderer = {
  Face?: ComponentType<{ item: StackItem }>;
  Detail?: ComponentType<{
    item: StackItem;
    extras?: Record<string, unknown>;
    onAction: (actionID: string, params?: unknown) => void;
    onRefresh: () => void;
    busy: boolean;
  }>;
};

const registry: Record<string, KindRenderer> = {
  'cards:decision': cardsDecision,
  'cards:briefing': cardsBriefing,
  'todo:todo': todoTodo,
};

export function rendererFor(item: Pick<StackItem, 'source_app' | 'kind'>): KindRenderer {
  return registry[`${item.source_app}:${item.kind}`] ?? {};
}
