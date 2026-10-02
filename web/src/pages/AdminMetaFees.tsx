import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import type { AdminTenant, MetaFeeStatement, MetaRate } from "../api/types";
import { formatPaise } from "../lib/billing";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

// PaymentMode switches who pays Meta for one workspace's messages.
export function PaymentMode({ id, mode }: { id: string; mode: AdminTenant["meta_payment_mode"] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [to, setTo] = useState(mode);
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", `/internal/admin/tenants/${id}/meta-payment-mode`, { mode: to, reason });
      setReason("");
      await qc.invalidateQueries({ queryKey: ["admin"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  return (
    <form className="card inline-form" onSubmit={save}>
      <label className="field">
        {t("admin.metaPaymentMode")}
        <select value={to} onChange={(e) => setTo(e.target.value as typeof mode)}>
          <option value="direct">{t("admin.mode_direct")}</option>
          <option value="through_us">{t("admin.mode_through_us")}</option>
        </select>
      </label>
      <label className="field grow">
        {t("admin.modeReason")}
        <input value={reason} onChange={(e) => setReason(e.target.value)} required minLength={5} maxLength={500} />
      </label>
      <button className="primary" disabled={to === mode}>{t("admin.modeSave")}</button>
      {error && <div className="error">{error}</div>}
    </form>
  );
}

const blank = { category: "marketing", country: "", rate: "", from: new Date().toISOString().slice(0, 10) };

// MetaFeesAdmin is the console tab: the rate card and every workspace's monthly statements.
export default function MetaFeesAdmin() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const rates = useQuery({
    queryKey: ["admin", "meta-rates"],
    queryFn: async () => (await api<{ data: MetaRate[] }>("GET", "/internal/admin/meta-rates")).data,
  });
  const statements = useQuery({
    queryKey: ["admin", "meta-statements"],
    queryFn: () => api<{ data: MetaFeeStatement[]; due_minor: number }>("GET", "/internal/admin/meta-fee-statements"),
  });
  const [form, setForm] = useState(blank);
  const [paying, setPaying] = useState<string | null>(null);
  const [ref, setRef] = useState("");
  const [why, setWhy] = useState("");
  const [error, setError] = useState("");

  const run = async (fn: () => Promise<unknown>, keys: string[]) => {
    setError("");
    try {
      await fn();
      for (const k of keys) await qc.invalidateQueries({ queryKey: ["admin", k] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const saveRate = (e: FormEvent) => {
    e.preventDefault();
    return run(async () => {
      await api("PUT", "/internal/admin/meta-rates", {
        category: form.category, country: form.country.trim().toUpperCase(), rate_hundredths: Number(form.rate), effective_from: form.from,
      });
      setForm({ ...blank, category: form.category });
    }, ["meta-rates"]);
  };
  const markPaid = (e: FormEvent) => {
    e.preventDefault();
    return run(async () => {
      await api("POST", `/internal/admin/meta-fee-statements/${paying}/status`, { status: "paid", payment_reference: ref, reason: why });
      setPaying(null);
      setRef("");
      setWhy("");
    }, ["meta-statements"]);
  };

  return (
    <>
      <h3>{t("admin.rateCard")}</h3>
      <p className="muted small">{t("admin.rateHelp")}</p>
      <div className="card table-wrap">
        <table>
          <thead>
            <tr><th>{t("admin.rateCategory")}</th><th>{t("admin.rateCountry")}</th><th>{t("admin.rateValue")}</th><th>{t("admin.rateFrom")}</th><th /></tr>
          </thead>
          <tbody>
            {rates.data?.map((r) => (
              <tr key={r.id}>
                <td>{r.category}</td>
                <td>{r.country}</td>
                <td>{r.rate_hundredths} <span className="muted small">(₹{(r.rate_hundredths / 10000).toFixed(4)})</span></td>
                <td>{r.effective_from}</td>
                <td><button className="link" onClick={() => run(() => api("DELETE", `/internal/admin/meta-rates/${r.id}`), ["meta-rates"])}>{t("admin.rateDelete")}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <form className="card inline-form" onSubmit={saveRate}>
        <label className="field">
          {t("admin.rateCategory")}
          <select value={form.category} onChange={(e) => setForm({ ...form, category: e.target.value })}>
            <option value="marketing">marketing</option>
            <option value="utility">utility</option>
            <option value="authentication">authentication</option>
          </select>
        </label>
        <label className="field">{t("admin.rateCountry")}<input value={form.country} onChange={(e) => setForm({ ...form, country: e.target.value })} required maxLength={2} /></label>
        <label className="field">{t("admin.rateValue")}<input type="number" min={0} value={form.rate} onChange={(e) => setForm({ ...form, rate: e.target.value })} required /></label>
        <label className="field">{t("admin.rateFrom")}<input type="date" value={form.from} onChange={(e) => setForm({ ...form, from: e.target.value })} required /></label>
        <button className="primary">{t("admin.rateAdd")}</button>
      </form>

      <h3>{t("admin.statementsTitle")}</h3>
      {statements.data && <p className="muted small">{t("admin.statementsDue", { total: formatPaise(statements.data.due_minor) })}</p>}
      <div className="card table-wrap">
        <table>
          <thead>
            <tr><th>{t("admin.invoiceNumber")}</th><th>{t("admin.workspace")}</th><th>{t("admin.invoiceTotalCol")}</th><th /></tr>
          </thead>
          <tbody>
            {statements.data?.data.map((s) => (
              <tr key={s.id}>
                <td>{s.number}<div className="small muted">{s.month}</div></td>
                <td>{s.tenant_name}</td>
                <td>
                  {formatPaise(s.total_minor)}
                  <div className="small muted">{t(`admin.statementStatus_${s.status}`)}{s.payment_reference ? ` · ${s.payment_reference}` : ""}</div>
                </td>
                <td className="actions">
                  <a href={`/internal/admin/meta-fee-statements/${s.id}/view`} target="_blank" rel="noreferrer">{t("admin.statementView")}</a>
                  {s.status === "due" && <button className="link" onClick={() => setPaying(s.id)}>{t("admin.markPaid")}</button>}
                </td>
              </tr>
            ))}
            {statements.isSuccess && statements.data.data.length === 0 && <tr><td colSpan={4} className="muted">{t("admin.noStatements")}</td></tr>}
          </tbody>
        </table>
      </div>
      {paying && (
        <form className="card inline-form" onSubmit={markPaid}>
          <label className="field">{t("admin.paymentRef")}<input value={ref} onChange={(e) => setRef(e.target.value)} required maxLength={100} /></label>
          <label className="field grow">{t("admin.paidReason")}<input value={why} onChange={(e) => setWhy(e.target.value)} required minLength={5} maxLength={500} /></label>
          <button className="primary">{t("admin.markPaid")}</button>
          <button type="button" onClick={() => setPaying(null)}>{t("common.cancel")}</button>
        </form>
      )}
      {error && <div className="error">{error}</div>}
    </>
  );
}
