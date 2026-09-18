import { BrowserRouter, Route, Routes } from "react-router-dom";
import { ConfirmProvider } from "./components/ConfirmDialog";
import Layout from "./components/Layout";
import { AuthProvider, RequireAuth } from "./lib/auth";
import { ToastProvider } from "./lib/toast";
import Blacklist from "./pages/Blacklist";
import ConfigEditor from "./pages/ConfigEditor";
import Dashboard from "./pages/Dashboard";
import Groups from "./pages/Groups";
import Login from "./pages/Login";
import Logs from "./pages/Logs";
import Restrictions from "./pages/Restrictions";
import Settings from "./pages/Settings";
import Users from "./pages/Users";

export default function App() {
  return (
    <ToastProvider>
      <ConfirmProvider>
        <AuthProvider>
          <BrowserRouter>
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route
                path="/"
                element={
                  <RequireAuth>
                    <Layout>
                      <Dashboard />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/blacklist"
                element={
                  <RequireAuth>
                    <Layout>
                      <Blacklist />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/restrictions"
                element={
                  <RequireAuth>
                    <Layout>
                      <Restrictions />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/groups"
                element={
                  <RequireAuth>
                    <Layout>
                      <Groups />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/users"
                element={
                  <RequireAuth>
                    <Layout>
                      <Users />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/settings"
                element={
                  <RequireAuth>
                    <Layout>
                      <Settings />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/config"
                element={
                  <RequireAuth>
                    <Layout>
                      <ConfigEditor />
                    </Layout>
                  </RequireAuth>
                }
              />
              <Route
                path="/logs"
                element={
                  <RequireAuth>
                    <Layout>
                      <Logs />
                    </Layout>
                  </RequireAuth>
                }
              />
            </Routes>
          </BrowserRouter>
        </AuthProvider>
      </ConfirmProvider>
    </ToastProvider>
  );
}
