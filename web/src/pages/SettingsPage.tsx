import { Select, SelectTrigger, SelectValue, SelectContent, SelectGroup, SelectItem } from "../components/ui/select";
import { Panel } from "../components/Panel";
import { usePreferences, type FontSize, type Language, type Theme } from "../lib/preferences";

export function SettingsPage() {
  const {language, theme, fontSize, setLanguage, setTheme, setFontSize, t} = usePreferences();
  return <section className="route-page settings-page" aria-label={t("Settings")}>
    <div className="settings-intro">
      <h2>{t("General")}</h2>
      <p>{t("Changes are saved automatically in this browser.")}</p>
    </div>
    <Panel className="settings-panel">
      <div className="settings-row">
        <div className="settings-copy">
          <label htmlFor="settings-language">{t("Interface language")}</label>
          <p id="settings-language-description">{t("Choose the display language for the app interface.")}</p>
        </div>
        <Select value={language} onValueChange={value => setLanguage(value as Language)}>
          <SelectTrigger id="settings-language" className="settings-select" aria-describedby="settings-language-description"><SelectValue /></SelectTrigger>
          <SelectContent position="popper" align="end"><SelectGroup>
            <SelectItem value="en"><span lang="en">English</span></SelectItem>
            <SelectItem value="zh-CN"><span lang="zh-CN">中文</span></SelectItem>
          </SelectGroup></SelectContent>
        </Select>
      </div>
    </Panel>
    <Panel className="settings-panel">
      <div className="settings-row">
        <div className="settings-copy">
          <label htmlFor="settings-font-size">{t("Text size")}</label>
          <p id="settings-font-size-description">{t("Choose the main text size for the console.")}</p>
        </div>
        <Select value={fontSize} onValueChange={value => setFontSize(value as FontSize)}>
          <SelectTrigger id="settings-font-size" className="settings-select" aria-describedby="settings-font-size-description"><SelectValue /></SelectTrigger>
          <SelectContent position="popper" align="end"><SelectGroup>
            <SelectItem value="sm">{t("Small (12px)")}</SelectItem>
            <SelectItem value="md">{t("Medium (13px)")}</SelectItem>
            <SelectItem value="lg">{t("Large (14px)")}</SelectItem>
          </SelectGroup></SelectContent>
        </Select>
      </div>
    </Panel>
    <Panel className="settings-panel">
      <div className="settings-row">
        <div className="settings-copy">
          <label htmlFor="settings-theme">{t("Appearance")}</label>
          <p id="settings-theme-description">{t("Follow the system or choose a light or dark appearance.")}</p>
        </div>
        <Select value={theme} onValueChange={value => setTheme(value as Theme)}>
          <SelectTrigger id="settings-theme" className="settings-select" aria-describedby="settings-theme-description"><SelectValue /></SelectTrigger>
          <SelectContent position="popper" align="end"><SelectGroup>
            <SelectItem value="system">{t("Follow system")}</SelectItem>
            <SelectItem value="light">{t("Light")}</SelectItem>
            <SelectItem value="dark">{t("Dark")}</SelectItem>
          </SelectGroup></SelectContent>
        </Select>
      </div>
    </Panel>
  </section>;
}
