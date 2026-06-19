import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import Intake from './Intake';
import '../styles.css';

// The public intake page is a single self-contained form — no router, no Shell,
// no session bootstrap. Anyone with the link can load it.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Intake />
  </StrictMode>,
);
