import { useState } from "react";
import type { View } from "../lib/view";
import { F, type FeatureName } from "./FeatureContext";
import { ago } from "../lib/format";
import { I } from "./Icon";

type NavItem = [icon: string, label: string, href: string | null, feature?: FeatureName];

const nav: NavItem[] = [
  ["home", "Главная", "/"],
  ["notes", "Все страницы", "/?env=all"],
  ["affiliate", "Граф знаний", "/?graph=1"],
  ["star", "Избранное", null, "favorites"],
  ["clock", "Недавние", "/?recent=1"],
  ["brand-github", "GitHub", "/?github=1", "github"],
  ["api", "API и MCP", "/?api=1", "api"],
  ["settings", "Настройки", "/?settings=1", "settings"],
];
const ownerFeatures: (FeatureName | undefined)[] = ["github", "api", "settings", "favorites"];

function isSelected(href: string, v: View): boolean {
  return (href === "/" && !v.Query && !v.Selected && !v.Page && !v.GraphView && !v.RecentView && !v.GitHubView && !v.SettingsView && !v.ApiView) || (href === "/?env=all" && !!v.Selected && !v.Query && !v.Page) || (href === "/?graph=1" && v.GraphView) || (href === "/?recent=1" && v.RecentView) || (href === "/?github=1" && v.GitHubView) || (href === "/?settings=1" && v.SettingsView) || (href === "/?api=1" && v.ApiView);
}

const systems: [icon: string, name: string, active: (v: View) => boolean][] = [
  ["shield-check", "Wiki", () => true],
  ["shield-check", "MCP", (v) => !!v.MCPLastSeen && Date.now() - Date.parse(v.MCPLastSeen) < 15 * 60000],
  ["brand-github", "GitHub", (v) => v.GitHubCount > 0],
];

export function Sidebar({ v }: { v: View }) {
  const [menuOpen, setMenuOpen] = useState(false);
  return (
    <aside className="sidebar" onKeyDown={(event) => { if (event.key === "Escape" && menuOpen) setMenuOpen(false); }}>
      <a className="brand" href="/">
        <img src="/assets/veda-logo.png" alt="" className="brand-mark" />
        <span>
          <strong>VEDA</strong>
          <small>KNOWLEDGE BASE</small>
        </span>
      </a>
      <nav id="main-navigation" className={menuOpen ? "mobile-open" : ""} aria-label="Основная навигация" onClick={(event) => { if (event.target instanceof Element && event.target.closest(".nav-item")) setMenuOpen(false); }}>
        {nav.filter(([, , , feature]) => !v.Public || !ownerFeatures.includes(feature)).map(([icon, label, href, feature]) =>
          href ? (
            <a
              className={"nav-item " + (isSelected(href, v) ? "selected" : "")}
              href={href}
              key={label}
            >
              <I name={icon} />
              <span>{label}</span>
            </a>
          ) : feature ? (
            <F name={feature} className="nav-item" key={label}>
              <I name={icon} />
              <span>{label}</span>
            </F>
          ) : null,
        )}
      </nav>
      <button type="button" className="mobile-menu-toggle" aria-label="Меню" aria-controls="main-navigation" aria-expanded={menuOpen} onClick={() => setMenuOpen(!menuOpen)}>
        <I name={menuOpen ? "x" : "dots"} />
      </button>
      <div className="sidebar-footer">
      {!v.Public && <div className="sidebar-bottom">
        <section className="storage panel">
          <div>
            Хранилище (Markdown)
            <I name="git-branch" />
          </div>
          <p>
            <b>{v.Count}</b> страниц<span>Локально</span>
          </p>
          <div className="meter">
            <i />
          </div>
        </section>
        <section className="system panel">
          <div className="system-brand">
            <img src="/assets/veda-logo.png" alt="" />
            <div>
              VEDA Wiki<small>v0.1.0</small>
            </div>
            <I name="chevron-down" />
          </div>
          <dl>
            {systems.map(([icon, name, isActive]) => {
              const active = isActive(v);
              return (
                <div key={name}>
                  <dt>
                    <I name={icon} />
                    {name}
                  </dt>
                  <dd className={active ? "" : "muted"}>
                    {active && <i />}
                    {name === "MCP" ? (v.MCPLastSeen ? ago(v.MCPLastSeen) : "Нет обращений") : active ? (name === "GitHub" ? "Настроен" : "Online") : "Не настроен"}
                  </dd>
                </div>
              );
            })}
          </dl>
        </section>
      </div>}
        <F className={"nav-item account" + (v.Public ? " sign-in" : "")} name="profile" label={v.Public ? "Войти" : "Профиль"}>
          <I name="user" />
          <span>{v.Public ? "Войти" : "Профиль"}</span>
        </F>
      </div>
    </aside>
  );
}
