// The landing page's languages: the countries with the most calibre installs
// (calibre-ebook.com/dynamic/calibre-usage). Docs and the changelog stay in
// English.
import en from "./en.json";

export type Strings = typeof en;

export interface Locale {
  /** The file in this folder, and the URL segment. */
  code: string;
  /** The landing page's path. */
  path: string;
  /** For <html lang> and hreflang. */
  lang: string;
  /** For og:locale. */
  og: string;
  /** The language's own name. */
  name: string;
}

export const locales: Locale[] = [
  { code: "en", path: "/", lang: "en", og: "en_US", name: "English" },
  { code: "es", path: "/es/", lang: "es", og: "es_ES", name: "Español" },
  { code: "de", path: "/de/", lang: "de", og: "de_DE", name: "Deutsch" },
  { code: "zh", path: "/zh/", lang: "zh-Hans", og: "zh_CN", name: "简体中文" },
  { code: "fr", path: "/fr/", lang: "fr", og: "fr_FR", name: "Français" },
  { code: "it", path: "/it/", lang: "it", og: "it_IT", name: "Italiano" },
  { code: "nl", path: "/nl/", lang: "nl", og: "nl_NL", name: "Nederlands" },
  { code: "pt-br", path: "/pt-br/", lang: "pt-BR", og: "pt_BR", name: "Português (Brasil)" },
];

export const defaultLocale = locales[0];

const files = import.meta.glob<{ default: unknown }>("./*.json", { eager: true });

// A translation missing a string falls back to English.
function merge(base: any, over: any): any {
  if (Array.isArray(base)) return Array.isArray(over) && over.length === base.length ? base.map((b, i) => merge(b, over[i])) : base;
  if (base && typeof base === "object") {
    const out: any = {};
    for (const k of Object.keys(base)) out[k] = merge(base[k], over?.[k]);
    return out;
  }
  return typeof over === typeof base && over !== "" ? over : base;
}

export function strings(code: string): Strings {
  return merge(en, files[`./${code}.json`]?.default);
}

export function locale(code: string): Locale {
  return locales.find((l) => l.code === code) ?? defaultLocale;
}

/** Fills "{name}" placeholders. */
export function fmt(text: string, vars: Record<string, string | number>): string {
  return text.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
}
