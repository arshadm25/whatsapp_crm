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
import ComingSoon from "./pages/ComingSoon";
import Templates from "./pages/Templates";
import NewTemplate from "./pages/NewTemplate";
import SendMessage from "./pages/SendMessage";
import Inbox from "./pages/Inbox";
import Developers from "./pages/Developers";
import Contacts from "./pages/Contacts";

function RequireAuth({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const me = useMe();
  const location = useLocation();
  if (me.isLoading) return <div className="center muted">{t("common.loading")}</div>;
  if (!me.data) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/signup" element={<Signup />} />
      <Route path="/verify-email" element={<VerifyEmail />} />
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
        {["campaigns", "settings"].map((p) => (
          <Route key={p} path={p} element={<ComingSoon section={p} />} />
        ))}
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
