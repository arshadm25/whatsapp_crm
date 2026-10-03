import { describe, expect, it } from "vitest";
import en from "./en.json";
import { LANGUAGES } from "../i18n";

type Tree = { [key: string]: string | Tree };

function flatten(t: Tree, prefix = ""): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(t)) {
    if (typeof v === "string") out[prefix + k] = v;
    else Object.assign(out, flatten(v, prefix + k + "."));
  }
  return out;
}

const placeholders = (s: string) => (s.match(/\{\{[^}]+\}\}/g) ?? []).sort();

const files = import.meta.glob<{ default: Tree }>("./*.json", { eager: true });
const base = flatten(en as Tree);

describe("locales", () => {
  for (const lang of LANGUAGES.filter((l) => l.code !== "en")) {
    it(`${lang.code} has every English key with the same placeholders`, () => {
      const file = files[`./${lang.code}.json`];
      expect(file, `${lang.code}.json missing`).toBeDefined();
      const tr = flatten(file!.default);
      expect(Object.keys(tr).sort()).toEqual(Object.keys(base).sort());
      for (const [k, v] of Object.entries(base)) {
        expect(placeholders(tr[k]!), `${lang.code}: ${k}`).toEqual(placeholders(v));
        expect(tr[k]!.trim(), `${lang.code}: ${k} is empty`).not.toBe("");
      }
    });
  }
});
