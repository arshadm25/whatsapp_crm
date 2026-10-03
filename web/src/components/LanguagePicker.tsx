import { useTranslation } from "react-i18next";
import { LANGUAGES, setLanguage } from "../i18n";
import Icon from "./Icon";

// LanguagePicker switches the dashboard language; the choice is kept in this browser.
export default function LanguagePicker() {
  const { t, i18n } = useTranslation();
  return (
    <label className="lang-picker">
      <Icon name="globe" size="s" />
      <select aria-label={t("nav.language")} value={i18n.resolvedLanguage ?? "en"} onChange={(e) => void setLanguage(e.target.value)}>
        {LANGUAGES.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
      </select>
    </label>
  );
}
