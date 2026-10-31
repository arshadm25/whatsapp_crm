import { useState } from "react";
import { Link, NavLink } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../api/client";
import { useMe, usePhoneNumbers } from "../../api/hooks";
import Icon, { type IconName } from "../../components/Icon";

// The title, actions and tabs shared by the three Numbers pages.
export default function NumbersHead() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const hasNumbers = !!usePhoneNumbers().data?.some((n) => n.status !== "disconnected");
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const sync = useMutation({
    mutationFn: () => api<{ synced: number }>("POST", "/internal/numbers/sync"),
    onSuccess: (r) => {
      setError(null);
      setNote(t("numbers.synced", { count: r.synced }));
      void qc.invalidateQueries({ queryKey: ["phone-numbers"] });
      void qc.invalidateQueries({ queryKey: ["profile"] });
    },
    onError: (e) => {
      setNote("");
      setError(e instanceof ApiError ? e.message : t("common.error"));
    },
  });
  const tabs = [
    { to: "/numbers", label: t("numbers.tabNumbers"), icon: "phone" as IconName, end: true },
    { to: "/numbers/profile", label: t("numbers.tabProfile"), icon: "building" as IconName, end: false },
    { to: "/numbers/limits", label: t("numbers.tabLimits"), icon: "chart" as IconName, end: false },
  ];

  return (
    <>
      <div className="page-head">
        <div>
          <h1>{t("numbers.title")}</h1>
          <p className="sub">{t("numbers.intro")}</p>
        </div>
        <div className="actions">
          {manager && hasNumbers && (
            <button onClick={() => sync.mutate()} disabled={sync.isPending}>
              <Icon name="refresh" size="s" />{sync.isPending ? t("numbers.syncing") : t("numbers.sync")}
            </button>
          )}
          <Link className="button primary" to="/numbers/connect"><Icon name="plus" size="s" />{t("numbers.connect")}</Link>
        </div>
      </div>
      {hasNumbers && (
        <nav className="segmented tabs dev-tabs" aria-label={t("numbers.title")}>
          {tabs.map((tab) => (
            <NavLink key={tab.to} to={tab.to} end={tab.end} className={({ isActive }) => (isActive ? "on" : "")}>
              <Icon name={tab.icon} size="s" />{tab.label}
            </NavLink>
          ))}
        </nav>
      )}
      {note && <div className="banner ok"><Icon name="check" size="s" /><div><span>{note}</span></div></div>}
      {error && <div className="field-error">{error}</div>}
    </>
  );
}
