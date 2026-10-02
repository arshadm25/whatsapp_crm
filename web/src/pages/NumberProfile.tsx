import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, ApiError } from "../api/client";
import type { BusinessProfile, PhoneNumber } from "../api/types";
import Icon from "../components/Icon";
import { initials } from "../lib/time";

const VERTICALS = ["UNDEFINED", "OTHER", "AUTO", "BEAUTY", "APPAREL", "EDU", "ENTERTAIN", "EVENT_PLAN", "FINANCE", "GROCERY",
  "GOVT", "HOTEL", "HEALTH", "NONPROFIT", "PROF_SERVICES", "RETAIL", "TRAVEL", "RESTAURANT", "NOT_A_BIZ"];

// NumberProfile is the business profile card on the Numbers screen: what customers see in WhatsApp.
export default function NumberProfile({ number, canEdit }: { number: PhoneNumber; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const path = `/v1/phone-numbers/${number.id}/profile`;
  const q = useQuery({ queryKey: ["profile", number.id], queryFn: () => api<BusinessProfile>("GET", path) });
  const [form, setForm] = useState<{ about: string; address: string; description: string; email: string; vertical: string; websites: string } | null>(null);
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

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
  const formId = `profile-${number.id}`;

  return (
    <div className="card flush">
      <div className="chd">
        <div><h2>{t("numbers.profileTitle")}</h2><p>{t("numbers.profileSub", { number: number.display_phone_number })}</p></div>
        {canEdit && (
          <span className="actions">
            {message && <span className={message.ok ? "muted small" : "field-error"}>{message.text}</span>}
            <button className="primary sm" form={formId} disabled={!form || save.isPending}>{t("numbers.saveChanges")}</button>
          </span>
        )}
      </div>
      {q.isLoading && <div className="cb muted">{t("common.loading")}</div>}
      {q.isError && <div className="cb field-error">{q.error instanceof ApiError ? q.error.message : t("common.error")}</div>}
      {form && (
        <form
          id={formId}
          className="cb profile-body"
          onSubmit={(e) => {
            e.preventDefault();
            setMessage(null);
            save.mutate();
          }}
        >
          <fieldset disabled={!canEdit} className="plain" style={{ display: "contents" }}>
            <div className="profile-logo">
              <span className="big">
                {q.data?.profile_picture_url ? <img src={q.data.profile_picture_url} alt="" /> : initials(number.verified_name ?? number.display_phone_number)}
              </span>
              {canEdit && (
                <>
                  <button type="button" className="sm" disabled={logo.isPending} onClick={() => fileInput.current?.click()}>
                    <Icon name="upload" size="xs" />{t("numbers.changeLogo")}
                  </button>
                  <input
                    ref={fileInput}
                    type="file"
                    accept="image/jpeg,image/png"
                    onChange={(e) => {
                      const f = e.target.files?.[0];
                      e.target.value = "";
                      if (f) {
                        setMessage(null);
                        logo.mutate(f);
                      }
                    }}
                  />
                </>
              )}
              <span className="hint">{t("numbers.logoHint")}</span>
            </div>
            <div className="grid g2 profile-fields">
              <label className="field">
                {t("numbers.about")}
                <input value={form.about} maxLength={139} onChange={set("about")} />
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
                <textarea value={form.websites} rows={2} onChange={set("websites")} style={{ minHeight: 40 }} />
              </label>
              <label className="field">
                {t("numbers.email")}
                <input type="email" value={form.email} maxLength={128} onChange={set("email")} />
              </label>
              <label className="field wide">
                {t("numbers.address")}
                <input value={form.address} maxLength={256} onChange={set("address")} />
              </label>
              <label className="field wide">
                {t("numbers.description")}
                <textarea value={form.description} maxLength={512} rows={3} onChange={set("description")} style={{ minHeight: 60 }} />
              </label>
            </div>
          </fieldset>
        </form>
      )}
    </div>
  );
}
