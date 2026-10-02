import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useLiveEvents, useMe } from "../api/hooks";
import type { Me } from "../api/types";

const NAV: { to: string; key: string; ready: boolean }[] = [
  { to: "/", key: "getStarted", ready: true },
  { to: "/inbox", key: "inbox", ready: true },
  { to: "/send", key: "send", ready: true },
  { to: "/templates", key: "templates", ready: true },
  { to: "/contacts", key: "contacts", ready: true },
  { to: "/campaigns", key: "campaigns", ready: true },
  { to: "/analytics", key: "analytics", ready: true },
  { to: "/numbers", key: "numbers", ready: true },
  { to: "/developers", key: "developers", ready: true },
  { to: "/settings", key: "settings", ready: true },
];

export default function Layout() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [resent, setResent] = useState(false);
  useLiveEvents();

  // Switching workspace changes every tenant-scoped query, so the cache starts over.
  const switchTo = async (tenantId: string) => {
    const next = await api<Me>("POST", "/internal/auth/switch-tenant", { tenant_id: tenantId });
    qc.clear();
    qc.setQueryData(["me"], next);
    navigate("/");
  };

  const logout = async () => {
    await api("POST", "/internal/auth/logout");
    qc.clear();
    navigate("/login");
  };

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">{t("app.name")}</div>
        {me.memberships.length > 1 ? (
          <select className="tenant-switch" aria-label={t("nav.switchWorkspace")} value={me.tenant?.id ?? ""} onChange={(e) => switchTo(e.target.value)}>
            {!me.tenant && <option value="" />}
            {me.memberships.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
          </select>
        ) : (
          <div className="tenant">{me.tenant?.name}</div>
        )}
        <nav>
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.to === "/"} className={({ isActive }) => (isActive ? "active" : "")}>
              {t(`nav.${n.key}`)}
              {!n.ready && <span className="soon">{t("nav.comingSoon")}</span>}
            </NavLink>
          ))}
          {me.user.is_platform_admin && (
            <NavLink to="/admin" className={({ isActive }) => (isActive ? "active" : "")}>{t("nav.admin")}</NavLink>
          )}
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
