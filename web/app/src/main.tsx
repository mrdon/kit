import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Stack from './Stack';
import StackItemDetail from './StackItemDetail';
import { BASENAME } from './workspace';
import './styles.css';

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
        <Route path="/" element={<Stack />} />
        <Route
          path="/stack/:source_app/:kind/:id"
          element={<StackItemDetail />}
        />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
