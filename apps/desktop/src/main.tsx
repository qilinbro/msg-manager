import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import WorkspacePreview from './WorkspacePreview';
import './styles.css';

const root = document.getElementById('root');
if (!root) throw new Error('工作台入口不存在');

const demoPage = new URLSearchParams(location.search).get('demo');
const demo =
  location.protocol === 'file:' || demoPage === 'away' || demoPage === 'wechat';
if (demo) document.title = '接班 · Away Desk';

createRoot(root).render(
  <StrictMode>
    {demo ? (
      <WorkspacePreview initialPage={demoPage === 'away' ? 'away' : 'wechat'} />
    ) : (
      <App />
    )}
  </StrictMode>,
);
