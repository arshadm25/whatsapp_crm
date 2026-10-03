import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import type { AppNotification } from "../api/types";
import Icon from "./Icon";

// NotificationBell shows the member's latest notifications; live events refresh it.
export default function NotificationBell() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const list = useQuery({
    queryKey: ["notifications"],
    queryFn: () => api<{ data: AppNotification[]; unread: number }>("GET", "/internal/notifications"),
    staleTime: 60_000,
  });
  const unread = list.data?.unread ?? 0;

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);

  const markRead = async (ids?: string[]) => {
    await api("POST", "/internal/notifications/read", ids ? { ids } : undefined);
    await qc.invalidateQueries({ queryKey: ["notifications"] });
  };
  const openOne = (n: AppNotification) => {
    setOpen(false);
    if (!n.read_at) void markRead([n.id]);
    if (n.link) navigate(n.link);
  };

  return (
    <div className="bell" ref={ref}>
      <button
        className="ib gh"
        aria-label={t("notifications.bell", { count: unread })}
        title={t("notifications.title")}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <Icon name="bell" size="s" />
        {unread > 0 && <span className="dot">{unread > 99 ? "99+" : unread}</span>}
      </button>
      {open && (
        <div className="bell-menu" role="dialog" aria-label={t("notifications.title")}>
          <div className="bell-head">
            <b>{t("notifications.title")}</b>
            {unread > 0 && <button className="link small" onClick={() => markRead()}>{t("notifications.markAll")}</button>}
          </div>
          {!list.data?.data.length && <p className="muted small bell-empty">{t("notifications.empty")}</p>}
          {list.data?.data.map((n) => (
            <button key={n.id} className={`bell-item${n.read_at ? "" : " unread"}`} onClick={() => openOne(n)}>
              <b>{n.title}</b>
              {n.body && <span className="small muted">{n.body}</span>}
              <span className="small muted">{new Date(n.created_at).toLocaleString()}</span>
            </button>
          ))}
          <button className="link small bell-foot" onClick={() => { setOpen(false); navigate("/settings?tab=notifications"); }}>
            {t("notifications.settingsLink")}
          </button>
        </div>
      )}
    </div>
  );
}
