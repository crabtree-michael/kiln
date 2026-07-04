import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Route, Routes } from 'react-router-dom';
import { App } from '@/App';
import { PrimaryScreen } from '@/components/PrimaryScreen';

const root = document.getElementById('root');
if (root === null) {
  throw new Error('root element #root is missing from index.html');
}

// `/` is the primary (08) screen; `/debug` keeps the original board+chat client
// (07) whole and unchanged as a developer view.
createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<PrimaryScreen />} />
        <Route path="/debug" element={<App />} />
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
