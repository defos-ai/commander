import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { initializeLocalization } from './i18n';
import './styles/theme.css';
import './styles/layout.css';

await initializeLocalization();
createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>);
