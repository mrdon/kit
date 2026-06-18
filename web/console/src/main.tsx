import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Shell from './Shell';
import Launcher from './Launcher';
import Integrations from './pages/Integrations';
import Netlify from './pages/Netlify';
import Widget from './pages/Widget';
import Tasks from './pages/Tasks';
import Expenses from './pages/Expenses';
import Roles from './pages/Roles';
import Admin from './pages/Admin';
import Vault from './pages/Vault';
import { BASENAME } from './workspace';
import './styles.css';
import '@chat/chat.css';

// The console is a desktop tool, not an installable PWA — it deliberately
// registers NO service worker and ships no manifest. The cards SW (scope
// /{slug}/) is taught to ignore /{slug}/web/* so it never caches console
// HTML (see web/app/public/sw.js).

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename={BASENAME}>
      <Routes>
        <Route element={<Shell />}>
          <Route path="/" element={<Launcher />} />
          <Route path="/tasks" element={<Tasks />} />
          <Route path="/expenses" element={<Expenses />} />
          <Route path="/admin" element={<Admin />} />
          <Route path="/roles" element={<Roles />} />
          <Route path="/vault" element={<Vault />} />
          <Route path="/integrations" element={<Integrations />} />
          <Route path="/netlify" element={<Netlify />} />
          <Route path="/widget" element={<Widget />} />
        </Route>
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
