import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { Boundary } from '../play/Boundary';
import './theme.css';
import './hoppers.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Boundary>
      <App />
    </Boundary>
  </StrictMode>,
);
