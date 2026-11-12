import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import AwayDesk from './AwayDesk';
import './styles.css';

const root = document.getElementById('root');
if (!root) throw new Error('工作台入口不存在');

const demo =
  location.protocol === 'file:' ||
  new URLSearchParams(location.search).get('demo') === 'away';
if (demo) document.title = '接班 · Away Desk';

createRoot(root).render(
  <StrictMode>{demo ? <AwayDesk demo /> : <App />}</StrictMode>,
);
