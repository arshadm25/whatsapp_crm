import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";

// Dashboard languages. Each name is written in its own script so people can find theirs.
// Messages and templates support any language; this list is only the interface.
export const LANGUAGES = [
  { code: "en", name: "English" },
  { code: "hi", name: "हिन्दी" },
  { code: "bn", name: "বাংলা" },
  { code: "ta", name: "தமிழ்" },
  { code: "te", name: "తెలుగు" },
  { code: "kn", name: "ಕನ್ನಡ" },
  { code: "ml", name: "മലയാളം" },
] as const;

const STORAGE_KEY = "ecogo.language";

function savedLanguage(): string {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v && LANGUAGES.some((l) => l.code === v)) return v;
  } catch {
    // storage blocked: fall back to English
  }
  return "en";
}

// Other languages load on first use, so English users do not download them.
const loaders = import.meta.glob<{ default: object }>(["./locales/*.json", "!./locales/en.json"]);

async function load(code: string) {
  const loader = loaders[`./locales/${code}.json`];
  if (!loader || i18n.hasResourceBundle(code, "translation")) return;
  i18n.addResourceBundle(code, "translation", (await loader()).default);
}

export async function setLanguage(code: string) {
  try {
    localStorage.setItem(STORAGE_KEY, code);
  } catch {
    // the choice then lasts until the page reloads
  }
  await load(code);
  await i18n.changeLanguage(code);
}

i18n.on("languageChanged", (lng) => {
  document.documentElement.lang = lng;
});

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en } },
  lng: "en",
  fallbackLng: "en",
  interpolation: { escapeValue: false },
});

const saved = savedLanguage();
if (saved !== "en") void setLanguage(saved);

export default i18n;
