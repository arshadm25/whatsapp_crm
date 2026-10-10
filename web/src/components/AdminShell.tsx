import { useState, type FormEvent, type ReactNode } from "react";
import { Link, NavLink, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useConfig, useMe } from "../api/hooks";
import type { AdminOverview, WebhookHealth } from "../api/types";
import { initials } from "../lib/time";
import Icon, { type IconName } from "./Icon";

export type AdminTab = "overview" | "tenants" | "plans" | "invoices" | "metaFees" | "webhooks" | "metaErrors" | "deletions" | "audit";

const NAV: { group: string; items: { tab: AdminTab; icon: IconName }[] }[] = [
  { group: "platform", items: [{ tab: "overview", icon: "grid" }, { tab: "tenants", icon: "building" }] },
  { group: "billing", items: [{ tab: "plans", icon: "file" }, { tab: "invoices", icon: "download" }, { tab: "metaFees", icon: "globe" }] },
  { group: "health", items: [{ tab: "webhooks", icon: "activity" }, { tab: "metaErrors", icon: "alert" }] },
  { group: "compliance", items: [{ tab: "deletions", icon: "x" }, { tab: "audit", icon: "clock" }] },
];

export const ADMIN_TABS: AdminTab[] = NAV.flatMap((g) => g.items.map((i) => i.tab));

export const adminPath = (tab: AdminTab) => (tab === "overview" ? "/admin" : `/admin/${tab}`);

// AdminShell is the staff-only frame of the admin console: a dark sidebar so it never looks
// like a customer workspace, a workspace search and the environment in the top bar.
export default function AdminShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const me = useMe().data!;
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const ready = me.user.is_platform_admin && me.user.two_factor_enabled;

  const overview = useQuery({
    queryKey: ["admin-overview", "24"],
    queryFn: () => api<AdminOverview>("GET", "/internal/admin/overview?hours=24"),
    refetchInterval: 60_000,
    enabled: ready,
  });
  const health = useQuery({
    queryKey: ["admin", "webhooks", "1"],
    queryFn: () => api<WebhookHealth>("GET", "/internal/admin/webhook-health?hours=1"),
    refetchInterval: 60_000,
    enabled: ready,
  });
  const failed = health.data?.hours.reduce((a, x) => a + x.failed, 0) ?? 0;
  const received = health.data?.hours.reduce((a, x) => a + x.received, 0) ?? 0;
  const counts: Partial<Record<AdminTab, number>> = {
    tenants: overview.data ? overview.data.tenants.active + overview.data.tenants.suspended : undefined,
    metaErrors: overview.data?.meta_errors.current || undefined,
  };
  const env = useConfig().data?.environment;

  const search = (e: FormEvent) => {
    e.preventDefault();
    navigate(`/admin/tenants${q.trim() ? `?q=${encodeURIComponent(q.trim())}` : ""}`);
  };
  const logout = async () => {
    await api("POST", "/internal/auth/logout");
    qc.clear();
    navigate("/login");
  };

  return (
    <div className="shell staff">
      <aside className="sidebar">
        <Link to="/admin" className="brand">
          <span className="logo"><img src="/ecogo-connect-logo-on-green.svg" alt="Ecogo Connect" /></span>
          <span className="btag">{t("adminNav.tag")}</span>
        </Link>
        <nav aria-label={t("adminNav.main")}>
          {NAV.map((g) => (
            <div key={g.group} style={{ display: "contents" }}>
              <span className="gl">{t(`adminNav.group_${g.group}`)}</span>
              {g.items.map((n) => (
                <NavLink key={n.tab} to={adminPath(n.tab)} end className={({ isActive }) => (isActive ? "active" : "")}>
                  <Icon name={n.icon} />
                  {t(`admin.tab_${n.tab}`)}
                  {counts[n.tab] !== undefined && (
                    <span className={n.tab === "metaErrors" ? "cnt am" : "cnt"}>{counts[n.tab]!.toLocaleString()}</span>
                  )}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        {ready && health.data && (
          <Link className="conn" to="/admin/webhooks">
            <b><span className={failed ? "gd off" : "gd"} />{failed ? t("adminNav.webhookFailures", { count: failed, formattedCount: failed.toLocaleString() }) : t("adminNav.allNormal")}</b>
            {t("adminNav.webhooksLastHour", { count: received, formattedCount: received.toLocaleString() })}
          </Link>
        )}
        <Link className="back" to="/">
          <Icon name="arrowRight" size="s" />
          {t("adminNav.backToWorkspace")}
        </Link>
        <div className="me">
          <span className="av">{initials(me.user.name || me.user.email)}</span>
          <span>
            <b>{me.user.name || me.user.email}</b>
            <small>{t("adminNav.staff")}</small>
          </span>
          <button className="ib gh" onClick={logout} aria-label={t("nav.logout")} title={t("nav.logout")}>
            <Icon name="logout" size="s" />
          </button>
        </div>
      </aside>
      <div className="main">
        <header className="top">
          <form className="search" onSubmit={search} role="search">
            <Icon name="search" size="s" />
            <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("adminNav.search")} aria-label={t("adminNav.search")} />
          </form>
          <span className="sp" />
          {env && <span className={`pill ${env === "production" ? "ok" : "wa"}`}>{t(`adminNav.env_${env}`, { defaultValue: env })}</span>}
        </header>
        <main className="content">{children}</main>
      </div>
    </div>
  );
}
