import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.tsx'
import './index.css'

// In production, suppress all non-error console output so tokens, internal
// state, and infrastructure details never appear in the browser console.
if (!import.meta.env.DEV) {
  const noop = () => {}
  console.log   = noop
  console.info  = noop
  console.warn  = noop
  console.debug = noop
  console.table = noop
  console.group = noop
  console.groupEnd = noop
  console.groupCollapsed = noop
  // console.error is intentionally left — used only for unrecoverable failures
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
