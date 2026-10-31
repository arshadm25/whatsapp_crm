import { useTranslation } from "react-i18next";

// ReasonField is the reason every staff action asks for; the server keeps it in the audit log
// and refuses one shorter than 5 or longer than 500 characters.
export default function ReasonField({ value, onChange, label }: { value: string; onChange: (v: string) => void; label?: string }) {
  const { t } = useTranslation();
  return (
    <label className="field">
      {label ?? t("admin.reason")}
      <input value={value} onChange={(e) => onChange(e.target.value)} required minLength={5} maxLength={500} placeholder={t("admin.reasonPlaceholder")} />
      <span className="hint">{t("admin.reasonHint")}</span>
    </label>
  );
}
