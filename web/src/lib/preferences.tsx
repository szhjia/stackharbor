import { createContext, useCallback, useContext, useLayoutEffect, useEffect, useState, type ReactNode } from "react";
import { zhCN } from "./translations";

export type Language = "en" | "zh-CN";
export type Theme = "system" | "light" | "dark";
export type FontSize = "sm" | "md" | "lg";
const languageKey = "stackharbor.language";
const themeKey = "stackharbor.theme";
const fontSizeKey = "stackharbor.font-size";
function read(key: string) {
  try { return localStorage.getItem(key); } catch { return null; }
}
function save(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* Preferences still apply without browser storage. */ }
}
function readFontSize(): FontSize {
  const value = read(fontSizeKey);
  return value === "md" || value === "lg" ? value : "sm";
}
function readTheme(): Theme {
  const value = read(themeKey);
  return value === "light" || value === "dark" ? value : "system";
}
function applyTheme(theme: Theme) {
  const dark = theme === "dark" || (theme === "system" && (window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false));
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
}
export function applyPreferences(language: Language, theme: Theme, fontSize: FontSize) {
  document.documentElement.lang = language;
  applyTheme(theme);
  document.documentElement.dataset.fontSize = fontSize;
}
export function initializePreferences() {
  applyPreferences(read(languageKey) === "zh-CN" ? "zh-CN" : "en", readTheme(), readFontSize());
}
type Translator = (text: string, values?: Record<string, string | number>) => string;
const interpolate: Translator = (text, values) => text.replace(/\{(\w+)\}/g, (match, key) => String(values?.[key] ?? match));
const Preferences = createContext({
  language: "en" as Language, theme: "system" as Theme, fontSize: "sm" as FontSize,
  setLanguage: (_value: Language) => {}, setTheme: (_value: Theme) => {}, setFontSize: (_value: FontSize) => {}, t: interpolate,
});
export function PreferencesProvider({children}: {children: ReactNode}) {
  const [language, updateLanguage] = useState<Language>(() => read(languageKey) === "zh-CN" ? "zh-CN" : "en");
  const [theme, updateTheme] = useState<Theme>(readTheme);
  const [fontSize, updateFontSize] = useState<FontSize>(readFontSize);
  useLayoutEffect(() => applyPreferences(language, theme, fontSize), [language, theme, fontSize]);
  useEffect(() => {
    if (theme !== "system" || !window.matchMedia) return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => applyTheme("system");
    media.addEventListener("change", update);
    update();
    return () => media.removeEventListener("change", update);
  }, [theme]);
  const t: Translator = useCallback((text, values) => interpolate(language === "zh-CN" ? zhCN[text] ?? text : text, values), [language]);
  return <Preferences.Provider value={{language, theme, fontSize, t,
    setLanguage: (value) => { updateLanguage(value); save(languageKey, value); },
    setTheme: (value) => { updateTheme(value); save(themeKey, value); },
    setFontSize: (value) => { updateFontSize(value); save(fontSizeKey, value); },
  }}>{children}</Preferences.Provider>;
}
export function usePreferences() { return useContext(Preferences); }
