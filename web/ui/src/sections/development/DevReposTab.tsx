// 開發 › Repositories(Swift 的 DevReposTab.swift)
import { useState } from "react";
import { Card, color, EmptyState, Pill } from "../../components/ui";
import { Seg, SIcon, Sparkline, t, useContextMenu, useList, useOps, type MenuItem, type PullRequest, type Repository, type Service } from "../../components/opsKit";
import { relative } from "../../fmt";
import { CoverageBar, isActivePR, useDev, VulnLabel } from "./devShared";

type SortKey = "lastCommit" | "name" | "coverage" | "openPRs" | "vulnerabilities" | "language";

export function DevReposTab() {
  const o = useOps();
  const dev = useDev();
  const [repos, err, ready] = useList<Repository>("repos");
  const [prs] = useList<PullRequest>("pull_requests");
  const [layout, setLayout] = useState<"grid" | "table">("grid");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: "lastCommit", asc: false });
  const [selection, setSelection] = useState<string>();
  const menu = useContextMenu();
  const q = query.toLowerCase();
  const rows = repos.filter((r) => !q || r.name.toLowerCase().includes(q) || r.language.toLowerCase().includes(q)).sort((a, b) => {
    const va = sort.key === "lastCommit" ? t(a.lastCommit) : (a as any)[sort.key];
    const vb = sort.key === "lastCommit" ? t(b.lastCommit) : (b as any)[sort.key];
    const c = typeof va === "string" ? va.localeCompare(vb) : va - vb;
    return sort.asc ? c : -c;
  });
  const setKey = (key: SortKey) => setSort({ key, asc: key === "name" || key === "coverage" || key === "language" });
  const vulns = repos.reduce((a, r) => a + r.vulnerabilities, 0);
  const repoMenu = (id: string): MenuItem[] => [
    { label: "查看 Pull Requests", onClick: () => dev.showRepoPRs(id) },
    { label: "前往 CI/CD", onClick: () => o.go("pipelines", id) },
  ];
  const th = (label: string, key?: SortKey, width?: number) => (
    <th style={{ width }} onClick={key ? () => setSort((s) => (s.key === key ? { key, asc: !s.asc } : { key, asc: key === "name" || key === "language" })) : undefined}>
      {label}{key && sort.key === key ? (sort.asc ? " ▲" : " ▼") : ""}
    </th>
  );
  return (
    <div className="ops-page">
      <div className="ops-filterbar">
        <input className="input" style={{ minWidth: 120, maxWidth: 220, flex: 1 }} placeholder="搜尋 Repo 或語言" value={query} onChange={(e) => setQuery(e.target.value)} />
        <select className="select" title="排序" value={["lastCommit", "name", "coverage", "openPRs", "vulnerabilities"].includes(sort.key) ? sort.key : "lastCommit"} onChange={(e) => setKey(e.target.value as SortKey)}>
          <option value="lastCommit">最近 commit</option><option value="name">名稱</option><option value="coverage">覆蓋率</option><option value="openPRs">進行中 PR</option><option value="vulnerabilities">弱點</option>
        </select>
        <span className="grow" />
        <span className="row gap12">
          <span className="muted">{repos.length} 個 Repo</span>
          {vulns > 0 && <span className="row gap4" style={{ color: "var(--red)" }}><SIcon name="exclamationmark.shield.fill" color="red" />{vulns} 個弱點</span>}
        </span>
        <Seg value={layout} onChange={setLayout} options={[{ value: "grid", label: <SIcon name="square.grid.2x2" />, title: "卡片" }, { value: "table", label: <SIcon name="list.bullet" />, title: "表格" }]} />
      </div>
      {!ready ? null : repos.length === 0 ? <EmptyState title={err ? "讀不到 Repository" : "沒有 Repository"} icon="shippingbox" message={err} />
        : layout === "grid" ? (
          <div className="page-scroll"><div className="page-inner">
            <div className="ops-grid-auto" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))", gap: 14 }}>
              {rows.map((r) => <RepoCard key={r.id} repo={r} prs={prs} services={o.services} onContextMenu={menu.open(repoMenu(r.id))} />)}
            </div>
          </div></div>
        ) : (
          <div className="ops-scroll">
            <table className="ops-table">
              <thead><tr>{th("名稱", "name")}{th("語言", "language", 100)}{th("覆蓋率", "coverage", 160)}{th("進行中 PR", "openPRs", 80)}{th("弱點", "vulnerabilities", 70)}{th("最近 commit", "lastCommit", 100)}{th("12 週 commit", undefined, 140)}</tr></thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} className={selection === r.id ? "sel" : ""} onClick={() => setSelection(r.id)} onDoubleClick={() => dev.showRepoPRs(r.id)} onContextMenu={menu.open(repoMenu(r.id))}>
                    <td><div className="col" style={{ gap: 1 }}><span style={{ fontWeight: 500 }}>{r.name}</span><span className="caption mono muted">{r.defaultBranch}</span></div></td>
                    <td>{r.language}</td>
                    <td><CoverageBar value={r.coverage} /></td>
                    <td className="num">{r.openPRs}</td>
                    <td><VulnLabel count={r.vulnerabilities} /></td>
                    <td className="muted">{relative(r.lastCommit)}</td>
                    <td><Sparkline values={r.weeklyCommits} c="blue" height={20} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      {menu.el}
    </div>
  );
}

function RepoCard({ repo, prs, services, onContextMenu }: { repo: Repository; prs: PullRequest[]; services: Service[]; onContextMenu: (e: React.MouseEvent) => void }) {
  const o = useOps();
  const dev = useDev();
  const svcs = services.filter((s) => s.repo === repo.id);
  const active = prs.filter((p) => p.repo === repo.id && isActivePR(p));
  const total = repo.weeklyCommits.reduce((a, b) => a + b, 0);
  const lastWeek = repo.weeklyCommits[repo.weeklyCommits.length - 1] ?? 0;
  const prevWeek = repo.weeklyCommits[repo.weeklyCommits.length - 2] ?? 0;
  const openCount = Math.max(repo.openPRs, active.length);
  const stat = (title: string, v: React.ReactNode, first?: boolean) => (
    <div className="col grow" style={{ gap: 2, paddingLeft: 8, borderLeft: first ? undefined : "1px solid var(--line)" }}>
      <span className="caption muted">{title}</span>{v}
    </div>
  );
  return (
    <div onContextMenu={onContextMenu}>
      <Card>
        <div className="col gap12">
          <div className="row gap8" style={{ alignItems: "flex-start" }}>
            <SIcon name="shippingbox.fill" color="blue" style={{ marginTop: 2 }} />
            <div className="col grow" style={{ gap: 1, minWidth: 0 }}>
              <span className="card-title ellipsis">{repo.name}</span>
              <span className="caption muted row gap4 ellipsis"><SIcon name="arrow.triangle.branch" size={11} /><span className="mono">{repo.defaultBranch}</span>・最近 commit {relative(repo.lastCommit)}</span>
            </div>
            <Pill text={repo.language} />
          </div>
          <div className="col gap4"><span className="caption muted">測試覆蓋率</span><CoverageBar value={repo.coverage} /></div>
          <div className="row" style={{ marginLeft: -8 }}>
            {stat("進行中 PR", <button className="ops-plain num" title="查看這個 Repo 的 PR" style={{ fontSize: 15, fontWeight: 600 }} onClick={() => dev.showRepoPRs(repo.id)}>{openCount}</button>, true)}
            {stat("弱點", <VulnLabel count={repo.vulnerabilities} big />)}
            {stat("本週 commit", <span className="row gap4" style={{ alignItems: "baseline" }}><span className="num" style={{ fontSize: 15, fontWeight: 600 }}>{lastWeek}</span>
              {prevWeek > 0 && <span className="caption" style={{ color: lastWeek - prevWeek >= 0 ? "var(--green)" : "var(--orange)" }}>{lastWeek - prevWeek >= 0 ? `+${lastWeek - prevWeek}` : lastWeek - prevWeek}</span>}</span>)}
          </div>
          <div className="col gap4">
            <div className="row"><span className="caption muted">最近 12 週 commit</span><span className="grow" /><span className="caption muted num">共 {total}</span></div>
            {repo.weeklyCommits.length === 0 ? <div className="caption faint" style={{ height: 34 }}>沒有資料</div> : <Sparkline values={repo.weeklyCommits} c="blue" height={34} />}
          </div>
          {svcs.length > 0 && (
            <div className="row gap6 wrap">
              <span className="caption muted">部署為</span>
              {svcs.slice(0, 3).map((s) => (
                <button key={s.id} className="ops-chip" style={{ fontSize: 11.5, padding: "2px 6px" }} title={`前往服務 ${s.name}`} onClick={() => o.go("infrastructure", s.id)}>
                  <span className="dot" style={{ width: 6, height: 6, background: color(s.health === "healthy" ? "green" : s.health === "degraded" ? "orange" : s.health === "down" ? "red" : s.health === "maintenance" ? "blue" : "gray") }} />{s.name}
                </button>
              ))}
              {svcs.length > 3 && <span className="caption muted">+{svcs.length - 3}</span>}
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
