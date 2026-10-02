import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useTranslation } from "react-i18next";
import type { ReactNode } from "react";
import { useMe } from "./api/hooks";
import Layout from "./components/Layout";
import Login from "./pages/Login";
import Signup from "./pages/Signup";
import VerifyEmail from "./pages/VerifyEmail";
import GetStarted from "./pages/GetStarted";
import Numbers from "./pages/Numbers";
import ConnectWhatsApp from "./pages/ConnectWhatsApp";
import Templates from "./pages/Templates";
import NewTemplate from "./pages/NewTemplate";
import SendMessage from "./pages/SendMessage";
import Inbox from "./pages/Inbox";
import Developers from "./pages/Developers";
import Contacts from "./pages/Contacts";
import Campaigns from "./pages/Campaigns";
import Bots from "./pages/Bots";
import Analytics from "./pages/Analytics";
import Settings from "./pages/Settings";
import AcceptInvite from "./pages/AcceptInvite";
import TwoStep from "./pages/TwoStep";
import Admin from "./pages/Admin";

function RequireAuth({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const me = useMe();
  const location = useLocation();
  if (me.isLoading) return <div className="center muted">{t("common.loading")}</div>;
  if (!me.data) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  if (me.data.mfa_required) return <Navigate to="/2fa" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/verify-email" element={<VerifyEmail />} />
      <Route path="/invite" element={<AcceptInvite />} />
      <Route path="/2fa" element={<TwoStep />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<GetStarted />} />
        <Route path="numbers" element={<Numbers />} />
        <Route path="numbers/connect" element={<ConnectWhatsApp />} />
        <Route path="templates" element={<Templates />} />
        <Route path="templates/new" element={<NewTemplate />} />
        <Route path="send" element={<SendMessage />} />
        <Route path="inbox" element={<Inbox />} />
        <Route path="inbox/:id" element={<Inbox />} />
        <Route path="developers" element={<Developers />} />
        <Route path="contacts" element={<Contacts />} />
        <Route path="campaigns" element={<Campaigns />} />
        <Route path="bots" element={<Bots />} />
        <Route path="analytics" element={<Analytics />} />
        <Route path="settings" element={<Settings />} />
        <Route path="admin" element={<Admin />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
