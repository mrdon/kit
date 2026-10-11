export * from './api/client';
export * from './api/core';
export * from './api/devices';
export * from './api/menu';
export * from './api/trivia';
export * from './api/events.types';
export * from './api/events';
export * from './api/posters.types';
export * from './api/posters';

import { coreApi } from './api/core';
import { devicesApi } from './api/devices';
import { menuApi } from './api/menu';
import { triviaApi } from './api/trivia';
import { eventsApi } from './api/events';
import { postersApi } from './api/posters';

// One object, so `api.someMethod` keeps working from every page. Each
// area owns its own slice in ./api/<area>.ts; this just spreads them.
export const api = {
  ...coreApi,
  ...devicesApi,
  ...menuApi,
  ...triviaApi,
  ...eventsApi,
  ...postersApi,
};
