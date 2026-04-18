import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Stack from './Stack';
import CardDetail from './CardDetail';
import './styles.css';

// Service worker is registered lazily so a dev build over HTTP localhost
// still boots cleanly. Only attempts registration when served from /app/.
if ('serviceWorker' in navigator && location.pathname.startsWith('/app/')) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/app/sw.js').catch(() => {});
  });
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename="/app">
      <Routes>
        <Route path="/" element={<Stack />} />
        <Route path="/cards/:id" element={<CardDetail />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
