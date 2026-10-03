import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useInboxCounts, useLiveEvents, useMe, usePhoneNumbers } from "../api/hooks";
import type { BillingOverview, Me } from "../api/types";
import { daysLeft } from "../lib/billing";
import Icon, { type IconName } from "./Icon";

type NavItem = { to: string; key: string; icon: IconName };

const NAV: { group: string; items: NavItem[] }[] = [
  {
    group: "overview",
    items: [
      { to: "/", key: "getStarted", icon: "grid" },
      { to: "/inbox", key: "inbox", icon: "inbox" },
      { to: "/analytics", key: "analytics", icon: "chart" },
    ],
  },
  {
    group: "messaging",
    items: [
      { to: "/send", key: "send", icon: "send" },
      { to: "/contacts", key: "contacts", icon: "users" },
      { to: "/campaigns", key: "campaigns", icon: "megaphone" },
      { to: "/templates", key: "templates", icon: "file" },
    ],
  },
  {
    group: "automation",
    items: [
      { to: "/bots", key: "bots", icon: "bot" },
      { to: "/flows", key: "flows", icon: "flow" },
      { to: "/knowledge", key: "knowledge", icon: "book" },
    ],
  },
  {
    group: "manage",
    items: [
      { to: "/numbers", key: "numbers", icon: "phone" },
      { to: "/developers", key: "developers", icon: "code" },
      { to: "/settings", key: "settings", icon: "settings" },
    ],
  },
];

const ALL_ITEMS = NAV.flatMap((g) => g.items);

function initials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase() || "?";
}

export default function Layout() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const numbers = usePhoneNumbers();
  const [resent, setResent] = useState(false);
  useLiveEvents();

  const connected = numbers.data?.find((n) => n.status === "connected");
  const unread = useInboxCounts().data?.unread_conversations ?? 0;
  const page = location.pathname.startsWith("/admin")
    ? "admin"
    : [...ALL_ITEMS].reverse().find((n) => (n.to === "/" ? location.pathname === "/" : location.pathname.startsWith(n.to)))?.key;

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
        <Link className="brand" to="/">
          <span className="logo"><img src="/ecogo-logo.webp" alt="Ecogo" /></span>
          <span className="btag">WhatsApp</span>
        </Link>
        <div className="ws">
          <span className="av">{initials(me.tenant?.name ?? "")}</span>
          <span className="t">
            <b>{me.tenant?.name}</b>
            {me.tenant?.role && <small>{t(`settings.role_${me.tenant.role}`)}</small>}
          </span>
          {me.memberships.length > 1 && (
            <>
              <Icon name="chevrons" size="s" />
              <select aria-label={t("nav.switchWorkspace")} value={me.tenant?.id ?? ""} onChange={(e) => switchTo(e.target.value)}>
                {!me.tenant && <option value="" />}
                {me.memberships.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
              </select>
            </>
          )}
        </div>
        <nav aria-label={t("nav.main")}>
          {NAV.map((g) => (
            <div key={g.group} style={{ display: "contents" }}>
              <span className="gl">{t(`nav.group_${g.group}`)}</span>
              {g.items.map((n) => (
                <NavLink key={n.to} to={n.to} end={n.to === "/"} className={({ isActive }) => (isActive ? "active" : "")}>
                  <Icon name={n.icon} />
                  {t(`nav.${n.key}`)}
                  {n.key === "inbox" && unread > 0 && <span className="cnt" aria-label={t("inbox.unreadBadge", { count: unread })}>{unread > 99 ? "99+" : unread}</span>}
                </NavLink>
              ))}
              {g.group === "manage" && me.user.is_platform_admin && (
                <NavLink to="/admin" className={({ isActive }) => (isActive ? "active" : "")}>
                  <Icon name="shield" />
                  {t("nav.admin")}
                </NavLink>
              )}
            </div>
          ))}
        </nav>
        <Link className="conn" to={connected ? "/numbers" : "/numbers/connect"}>
          <b><span className={connected ? "gd" : "gd off"} />{connected ? t("nav.connected") : t("nav.notConnected")}</b>
          {connected
            ? `${connected.display_phone_number} · ${t("numbers.quality")} ${t(`numbers.quality_${connected.quality_rating}`)}`
            : t("nav.connectHint")}
        </Link>
        <div className="me">
          <span className="av">{initials(me.user.name || me.user.email)}</span>
          <span>
            <b>{me.user.name}</b>
            <small>{me.user.email}</small>
          </span>
          <button className="ib gh" onClick={logout} aria-label={t("nav.logout")} title={t("nav.logout")}>
            <Icon name="logout" size="s" />
          </button>
        </div>
      </aside>
      <div className="main">
        <header className="top">
          <span className="crumb">
            {me.tenant?.name}
            {page && <> / <b>{t(`nav.${page}`)}</b></>}
          </span>
          <span className="sp" />
          {connected && (
            <Link className="num" to="/numbers">
              <span className="gd" />
              {connected.display_phone_number}
            </Link>
          )}
        </header>
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
          {me.tenant?.role === "owner" && <PlanBanner />}
          <Outlet />
        </main>
      </div>
    </div>
  );
}

// PlanBanner warns the owner when sending has stopped or the trial is about to end.
function PlanBanner() {
  const { t } = useTranslation();
  const billing = useQuery({
    queryKey: ["billing"],
    queryFn: () => api<BillingOverview>("GET", "/internal/billing"),
    staleTime: 5 * 60_000,
  });
  const sub = billing.data?.subscription;
  if (!sub) return null;
  const days = daysLeft(sub.current_period_end);
  let text = "";
  if (!sub.usable) text = t("billing.bannerEnded");
  else if (sub.status === "past_due") text = t("billing.bannerPastDue");
  else if (sub.status === "trialing" && !billing.data?.plan && days <= 3) text = t("billing.bannerTrial", { count: days });
  if (!text) return null;
  return (
    <div className="banner">
      {text} <Link to="/settings?tab=billing">{t("billing.bannerAction")}</Link>
    </div>
  );
}
