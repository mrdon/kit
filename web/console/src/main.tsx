import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Shell from './Shell';
import Launcher from './Launcher';
import Integrations from './pages/Integrations';
import AppsSettings from './pages/AppsSettings';
import Netlify from './pages/Netlify';
import SquareShifts from './pages/SquareShifts';
import Widget from './pages/Widget';
import Tasks from './pages/Tasks';
import EmailIntakeSettings from './pages/EmailIntakeSettings';
import Expenses from './pages/Expenses';
import ExpenseSettings from './pages/ExpenseSettings';
import Roles from './pages/Roles';
import Admin from './pages/Admin';
import Vault from './pages/Vault';
import Skills from './pages/Skills';
import Jobs from './pages/Jobs';
import Connect from './pages/Connect';
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
          <Route path="/tasks/email-intake" element={<EmailIntakeSettings />} />
          <Route path="/tasks/:id" element={<Tasks />} />
          <Route path="/expenses" element={<Expenses />} />
          <Route path="/expenses/:id" element={<Expenses />} />
          <Route path="/vault" element={<Vault />} />
          <Route path="/vault/:id" element={<Vault />} />
          <Route path="/skills" element={<Skills />} />
          <Route path="/skills/:id" element={<Skills />} />
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/jobs/:id" element={<Jobs />} />
          <Route path="/connect" element={<Connect />} />
          <Route path="/admin" element={<Admin />} />
          <Route path="/admin/apps" element={<AppsSettings />} />
          <Route path="/admin/expenses" element={<ExpenseSettings />} />
          <Route path="/admin/roles" element={<Roles />} />
          <Route path="/admin/integrations" element={<Integrations />} />
          <Route path="/admin/netlify" element={<Netlify />} />
          <Route path="/admin/square-shifts" element={<SquareShifts />} />
          <Route path="/admin/widget" element={<Widget />} />
        </Route>
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
