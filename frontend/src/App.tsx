import type { ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { ConfirmProvider } from "./components/ConfirmDialog";
import { ErrorBoundary } from "./components/ErrorBoundary";
import Layout from "./components/Layout";
import { WriteGuard } from "./components/WriteGuard";
import { AuthProvider, RequireAuth, type Role } from "./lib/auth";
import { ToastProvider } from "./lib/toast";
import AccessRules from "./pages/AccessRules";
import Alerts from "./pages/Alerts";
import Audit from "./pages/Audit";
import Blacklist from "./pages/Blacklist";
import Blocklists from "./pages/Blocklists";
import ChangePassword from "./pages/ChangePassword";
import ConfigEditor from "./pages/ConfigEditor";
import Dashboard from "./pages/Dashboard";
import Groups from "./pages/Groups";
import History from "./pages/History";
import Limits from "./pages/Limits";
import Login from "./pages/Login";
import Logs from "./pages/Logs";
import PanelUsers from "./pages/PanelUsers";
import Restrictions from "./pages/Restrictions";
import Settings from "./pages/Settings";
import Stats from "./pages/Stats";
import SquidSettings from "./pages/SquidSettings";
import Users from "./pages/Users";

interface PageRoute {
  path: string;
  element: ReactNode;
  /** Lowest role allowed to open the page. */
  min?: Role;
  /** Make the page read-only for roles below operator. */
  guard?: boolean;
}

// The minimum roles mirror the API's route groups; the API is what actually
// enforces them.
const pages: PageRoute[] = [
  { path: "/", element: <Dashboard /> },
  { path: "/blacklist", element: <Blacklist />, guard: true },
  { path: "/access", element: <AccessRules />, guard: true },
  { path: "/blocklists", element: <Blocklists />, guard: true },
  { path: "/restrictions", element: <Restrictions />, guard: true },
  { path: "/groups", element: <Groups />, guard: true },
  { path: "/users", element: <Users />, guard: true },
  { path: "/limits", element: <Limits />, min: "operator" },
  { path: "/stats", element: <Stats />, min: "operator" },
  { path: "/logs", element: <Logs />, min: "operator" },
  { path: "/squid-settings", element: <SquidSettings />, min: "operator" },
  { path: "/config", element: <ConfigEditor />, min: "admin" },
  { path: "/history", element: <History />, min: "admin" },
  { path: "/panel-users", element: <PanelUsers />, min: "admin" },
  { path: "/audit", element: <Audit />, min: "admin" },
  { path: "/alerts", element: <Alerts />, min: "admin" },
  { path: "/settings", element: <Settings /> },
];

export default function App() {
  return (
    <ErrorBoundary>
    <ToastProvider>
      <ConfirmProvider>
        <AuthProvider>
          <BrowserRouter>
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route
                path="/change-password"
                element={
                  <RequireAuth>
                    <ChangePassword />
                  </RequireAuth>
                }
              />
              {pages.map((p) => (
                <Route
                  key={p.path}
                  path={p.path}
                  element={
                    <RequireAuth min={p.min}>
                      <Layout>{p.guard ? <WriteGuard>{p.element}</WriteGuard> : p.element}</Layout>
                    </RequireAuth>
                  }
                />
              ))}
              {/* An unknown address used to render nothing at all. */}
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </BrowserRouter>
        </AuthProvider>
      </ConfirmProvider>
    </ToastProvider>
    </ErrorBoundary>
  );
}
