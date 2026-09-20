import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
// Fonts are bundled: the panel must not call out to Google (privacy, air-gapped
// servers) and a strict Content-Security-Policy needs every resource local.
import '@fontsource-variable/inter'
import '@fontsource-variable/jetbrains-mono'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
