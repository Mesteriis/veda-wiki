import type { GitHubAccount, GitHubConnection } from "../../api/types";
import { I } from "../../components/Icon";

interface AccountsProps {
  accounts: GitHubAccount[];
  connections: GitHubConnection[];
  selectedId: string | undefined;
  onSelect: (id: string) => void;
}

// Accounts with their repositories, plus connections without a known account.
export function Accounts({ accounts, connections, selectedId, onSelect }: AccountsProps) {
  const repo = (connection: GitHubConnection) => <button type="button" className={selectedId === connection.id ? "github-repo active" : "github-repo"} key={connection.id} onClick={() => onSelect(connection.id)}><I name="notes" />{connection.repository}</button>;
  const orphans = connections.filter((connection) => !accounts.some((account) => account.id === connection.account_id));
  return <aside className="github-accounts panel"><h2>Подключённые аккаунты</h2>{accounts.map((account) => <section className="github-account" key={account.id}><strong><I name="brand-github" />{account.login || account.id}</strong><small>{account.id}</small>{connections.filter((connection) => connection.account_id === account.id).map(repo)}</section>)}{orphans.length > 0 && <section className="github-account"><strong>Другие подключения</strong>{orphans.map(repo)}</section>}{!accounts.length && !connections.length && <p>Аккаунты и репозитории ещё не подключены.</p>}</aside>;
}
