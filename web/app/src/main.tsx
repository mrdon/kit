import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Stack from './Stack';
import StackItemDetail from './StackItemDetail';
import ToastViewport from './toast/ToastViewport';
import { BASENAME } from './workspace';
import { StackViews } from './types';
import { VIEW_PATHS } from './stack/views';
import './styles.css';
import '@chat/chat.css';

// Service worker is registered lazily so a dev build over HTTP localhost
// still boots cleanly. Scope comes from the SW URL's path; the SW itself
// derives the workspace slug from self.registration.scope at install.
if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register(BASENAME + '/sw.js').catch(() => {});
  });
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename={BASENAME}>
      <Routes>
        {/* Keyed per view so switching views remounts the stack: each
            view starts from its own list and its own scroll position. */}
        <Route
          path={VIEW_PATHS[StackViews.feed]}
          element={<Stack key="feed" view={StackViews.feed} />}
        />
        <Route
          path={VIEW_PATHS[StackViews.tasks]}
          element={<Stack key="tasks" view={StackViews.tasks} />}
        />
        <Route
          path="/stack/:source_app/:kind/:id"
          element={<StackItemDetail />}
        />
      </Routes>
      <ToastViewport />
    </BrowserRouter>
  </StrictMode>,
);
