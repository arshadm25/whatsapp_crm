import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useLiveEvents, useMe } from "../api/hooks";

const NAV: { to: string; key: string; ready: boolean }[] = [
  { to: "/", key: "getStarted", ready: true },
  { to: "/inbox", key: "inbox", ready: true },
  { to: "/send", key: "send", ready: true },
  { to: "/templates", key: "templates", ready: true },
  { to: "/contacts", key: "contacts", ready: true },
  { to: "/campaigns", key: "campaigns", ready: true },
  { to: "/numbers", key: "numbers", ready: true },
  { to: "/developers", key: "developers", ready: true },
  { to: "/settings", key: "settings", ready: false },
];

export default function Layout() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [resent, setResent] = useState(false);
  useLiveEvents();

  const logout = async () => {
    await api("POST", "/internal/auth/logout");
    qc.clear();
    navigate("/login");
  };

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">{t("app.name")}</div>
        <div className="tenant">{me.tenant?.name}</div>
        <nav>
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === "/"} className={({ isActive }) => (isActive ? "active" : "")}>
              {t(`nav.${n.key}`)}
              {!n.ready && <span className="soon">{t("nav.comingSoon")}</span>}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">
          <div className="muted small">{me.user.email}</div>
          <button className="link" onClick={logout}>
            {t("nav.logout")}
          </button>
        </div>
      </aside>
      <main className="content">
        {!me.user.email_verified && (
          <div className="banner">
            {t("auth.verifyBanner", { email: me.user.email })}{" "}
            {resent ? (
              <span className="muted">{t("auth.resent")}</span>
            ) : (
              <button
                className="link"
                onClick={async () => {
                  await api("POST", "/internal/auth/resend-verification");
                  setResent(true);
                }}
              >
                {t("auth.resend")}
              </button>
            )}
          </div>
        )}
        <Outlet />
      </main>
    </div>
  );
}
