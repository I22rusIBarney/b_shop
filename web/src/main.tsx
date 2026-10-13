import { StrictMode, lazy, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles.css'
import { AppProvider, useApp } from './lib/app'
import { PageLoader, ToastProvider } from './ui/kit'
import { ClientShell } from './ui/shell'
import Login from './pages/Login'
import AuthDone from './pages/AuthDone'
import Home from './pages/Home'
import Buy from './pages/Buy'
import PaymentStatus from './pages/PaymentStatus'
import Install from './pages/Install'
import Profile from './pages/Profile'
import Support from './pages/Support'

const Admin = lazy(() => import('./pages/admin/Admin'))

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { me } = useApp()
  const loc = useLocation()
  if (!me) return <Navigate to={`/login?return=${encodeURIComponent(loc.pathname + loc.search)}`} replace />
  return <>{children}</>
}

function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { me } = useApp()
  if (!me) return <Navigate to="/login?return=/admin" replace />
  if (!me.customer.is_admin) return <Navigate to="/" replace />
  return <>{children}</>
}

function Failed({ msg, retry }: { msg: string; retry: () => void }) {
  return (
    <div className="page-center" style={{ padding: 24 }}>
      <div className="stack" style={{ maxWidth: 340 }}>
        <h2>Не удалось открыть кабинет</h2>
        <p className="muted">{msg}</p>
        <button className="btn" onClick={retry}>
          Попробовать снова
        </button>
      </div>
    </div>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ToastProvider>
      <AppProvider fallback={<PageLoader />} failed={(m, r) => <Failed msg={m} retry={r} />}>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/auth/done" element={<AuthDone />} />
            <Route element={<ClientShell />}>
              <Route path="/payment/:id" element={<PaymentStatus />} />
            </Route>
            <Route
              path="/admin/*"
              element={
                <RequireAdmin>
                  <Suspense fallback={<PageLoader />}>
                    <Admin />
                  </Suspense>
                </RequireAdmin>
              }
            />
            <Route
              element={
                <RequireAuth>
                  <ClientShell />
                </RequireAuth>
              }
            >
              <Route index element={<Home />} />
              <Route path="buy" element={<Buy />} />
              <Route path="install/:id" element={<Install />} />
              <Route path="profile" element={<Profile />} />
              <Route path="support" element={<Support />} />
            </Route>
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </BrowserRouter>
      </AppProvider>
    </ToastProvider>
  </StrictMode>,
)
