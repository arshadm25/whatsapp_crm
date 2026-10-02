import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, ApiError } from "../api/client";
import type { BusinessProfile, PhoneNumber } from "../api/types";

const VERTICALS = ["UNDEFINED", "OTHER", "AUTO", "BEAUTY", "APPAREL", "EDU", "ENTERTAIN", "EVENT_PLAN", "FINANCE", "GROCERY",
  "GOVT", "HOTEL", "HEALTH", "NONPROFIT", "PROF_SERVICES", "RETAIL", "TRAVEL", "RESTAURANT", "NOT_A_BIZ"];

export default function NumberProfile({ number, onClose }: { number: PhoneNumber; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const path = `/v1/phone-numbers/${number.id}/profile`;
  const q = useQuery({ queryKey: ["profile", number.id], queryFn: () => api<BusinessProfile>("GET", path) });
  const [form, setForm] = useState<{ about: string; address: string; description: string; email: string; vertical: string; websites: string } | null>(null);
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    if (q.data) {
      setForm({
        about: q.data.about ?? "", address: q.data.address ?? "", description: q.data.description ?? "",
        email: q.data.email ?? "", vertical: q.data.vertical || "UNDEFINED", websites: (q.data.websites ?? []).join("\n"),
      });
    }
  }, [q.data]);

  const fail = (e: unknown) => setMessage({ ok: false, text: e instanceof ApiError ? e.message : t("common.error") });
  const save = useMutation({
    mutationFn: () =>
      api<BusinessProfile>("PATCH", path, {
        ...form,
        websites: form!.websites.split("\n").map((w) => w.trim()).filter(Boolean),
      }),
    onSuccess: (p) => {
      qc.setQueryData(["profile", number.id], p);
      setMessage({ ok: true, text: t("numbers.saved") });
    },
    onError: fail,
  });
  const logo = useMutation({
    mutationFn: (file: File) => {
      const fd = new FormData();
      fd.append("file", file);
      return api<BusinessProfile>("POST", `${path}/logo`, fd);
    },
    onSuccess: (p) => {
      qc.setQueryData(["profile", number.id], p);
      setMessage({ ok: true, text: t("numbers.saved") });
    },
    onError: fail,
  });

  const set = (k: keyof NonNullable<typeof form>) => (e: { target: { value: string } }) =>
    setForm((f) => (f ? { ...f, [k]: e.target.value } : f));

  return (
    <div className="card" style={{ marginTop: "1rem" }}>
      <h2>{t("numbers.profileTitle", { number: number.display_phone_number })}</h2>
      <p className="muted">{t("numbers.profileIntro")}</p>
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {q.isError && <div className="field-error">{q.error instanceof ApiError ? q.error.message : t("common.error")}</div>}
      {form && (
        <form
          className="stack"
          onSubmit={(e) => {
            e.preventDefault();
            setMessage(null);
            save.mutate();
          }}
        >
          <div className="field">
            <span>{t("numbers.logo")}</span>
            {q.data?.profile_picture_url && <img src={q.data.profile_picture_url} alt="" width={64} height={64} style={{ borderRadius: 8 }} />}
            <input
              type="file"
              accept="image/jpeg,image/png"
              disabled={logo.isPending}
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) {
                  setMessage(null);
                  logo.mutate(f);
                }
              }}
            />
            <span className="muted small">{t("numbers.logoHint")}</span>
          </div>
          <label className="field">
            {t("numbers.about")}
            <input value={form.about} maxLength={139} onChange={set("about")} />
          </label>
          <label className="field">
            {t("numbers.description")}
            <textarea value={form.description} maxLength={512} rows={3} onChange={set("description")} />
          </label>
          <label className="field">
            {t("numbers.address")}
            <input value={form.address} maxLength={256} onChange={set("address")} />
          </label>
          <label className="field">
            {t("numbers.email")}
            <input type="email" value={form.email} maxLength={128} onChange={set("email")} />
          </label>
          <label className="field">
            {t("numbers.vertical")}
            <select value={form.vertical} onChange={set("vertical")}>
              {VERTICALS.map((v) => (
                <option key={v} value={v}>{t(`numbers.verticals.${v}`)}</option>
              ))}
            </select>
          </label>
          <label className="field">
            {t("numbers.websites")}
            <textarea value={form.websites} rows={2} onChange={set("websites")} />
          </label>
          {message && <div className={message.ok ? "muted" : "field-error"}>{message.text}</div>}
          <div className="row">
            <button className="primary" disabled={save.isPending}>{t("numbers.save")}</button>
            <button type="button" onClick={onClose}>{t("common.cancel")}</button>
          </div>
        </form>
      )}
    </div>
  );
}
