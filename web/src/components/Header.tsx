import type { View } from "../lib/view";
import { F } from "./FeatureContext";
import { I } from "./Icon";

function heading(v: View): [title: string, subtitle: string] {
  if (v.SettingsView) return ["Настройки", "Управление пользователями, командами и ключами доступа"];
  if (v.GitHubView) return ["Интеграции GitHub", "Подключение репозиториев и синхронизация с базой знаний"];
  if (v.RecentView) return ["Недавние", "Страницы, которые вы недавно открывали"];
  if (v.GraphView) return ["Граф знаний", "Связи между статьями базы знаний по источникам"];
  if (v.Selected && !v.Query && !v.Page) return ["Все статьи", "Полный каталог статей базы знаний"];
  return ["Вики VEDA", "База знаний для работы, проектов и документации"];
}

export function Header({ v }: { v: View }) {
  const [title, subtitle] = heading(v);
  return (
    <header className="masthead">
      <div className="masthead-title">
        <h1>{title}</h1>
        <p>{subtitle}</p>
      </div>
      <form action="/" className="global-search" role="search">
        <label className="sr" htmlFor="search">
          Поиск по вики
        </label>
        <I name="search" />
        <input
          id="search"
          name="q"
          placeholder="Поиск по вики..."
          defaultValue={v.Query}
        />
        <kbd>⌘</kbd>
        <kbd>K</kbd>
      </form>
      <div className="header-actions">
        {!v.Public && <><span className="reading-mode"><I name="shield-check" />Только чтение</span>
        <F className="round" name="notifications" label="Уведомления">
          <I name="bell" />
        </F>
        <F className="round" name="theme" label="Оформление">
          <I name="sun" />
        </F></>}
        {/* On wider screens the sign-in and profile button lives at the foot of the sidebar. */}
        {v.Public ? <F className="login-button header-account" name="profile" label="Войти">Войти</F> : <F className="round profile header-account" name="profile" label="Профиль">V</F>}
      </div>
    </header>
  );
}
