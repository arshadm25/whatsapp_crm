import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import type { MetaFeeOverview } from "../api/types";
import { formatPaise } from "../lib/billing";

// MetaFees is the billing page's section on Meta's message fees. Most workspaces pay Meta
// directly and see only a note; a workspace that pays through Ecogo sees its statements.
export default function MetaFees() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["billing", "meta-fees"],
    queryFn: () => api<MetaFeeOverview>("GET", "/internal/billing/meta-fees"),
  });
  const o = q.data;
  if (!o) return null;
  if (o.mode !== "through_us") return <p className="muted small">{t("billing.metaFees")}</p>;
  const m = o.month_to_date;
  return (
    <>
      <h2>{t("billing.metaFeesTitle")}</h2>
      <p className="muted small">
        {t("billing.metaFeesThroughUs", { since: o.since ? t("billing.metaFeesSince", { date: new Date(o.since).toLocaleDateString() }) : "" })}
      </p>
      {!o.invoicing && <div className="card muted small">{t("billing.metaFeesNoInvoicing")}</div>}
      {m && (
        <p className="small">
          {t("billing.metaFeesMonth", { month: m.month, fee: formatPaise(m.fee_minor), total: formatPaise(m.estimate_minor) })}
          {m.unrated_messages > 0 && <> {t("billing.metaFeesUnrated", { count: m.unrated_messages })}</>}
        </p>
      )}
      <h3>{t("billing.metaFeesStatements")}</h3>
      {o.statements.length === 0 ? (
        <div className="card muted">{t("billing.metaFeesNone")}</div>
      ) : (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr><th>{t("billing.metaFeesMonthCol")}</th><th>{t("admin.invoiceNumber")}</th><th>{t("admin.invoiceTotalCol")}</th><th /></tr>
            </thead>
            <tbody>
              {o.statements.map((s) => (
                <tr key={s.id}>
                  <td>{s.month}</td>
                  <td className="small">{s.number}</td>
                  <td>
                    {formatPaise(s.total_minor)}
                    <div className="muted small">{t(`billing.metaFees${s.status === "paid" ? "Paid" : s.status === "void" ? "Void" : "Due"}`)}</div>
                  </td>
                  <td><a href={`/internal/billing/meta-fees/statements/${s.id}/view`} target="_blank" rel="noreferrer">{t("billing.metaFeesView")}</a></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
