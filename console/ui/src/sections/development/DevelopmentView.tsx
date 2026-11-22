// 開發進度:概覽 / 看板 / Issue / Pull Requests / Repositories(Swift 的 DevelopmentView.swift)
// Repositories 是 GitHub 與 GitLab 追蹤中的 repo(原本的 GitHub、GitLab 分頁併進來),只看資料;
// 連線、登入與追蹤的 repo / 專案在 設定 › 程式碼倉庫(sections/settings/SettingsRepos.tsx)。
import { useEffect, useState } from "react";
import { Segmented } from "../../components/Segmented";
import { SIcon } from "../../components/SIcon";
import { OmixorProvider } from "../../lib/directory";
import { useList } from "../../lib/hooks";
import type { PullRequest, Repository, WorkItem } from "../../models";
import type { SectionProps } from "..";
import { DEV_TABS, DevCtx, type DevModel, type DevTab } from "./devShared";
import { DevOverviewTab } from "./DevOverviewTab";
import { DevBoardTab } from "./DevBoardTab";
import { DevIssuesTab, type Issue } from "./DevIssuesTab";
import { findIssue, KEY_RE, UUID_RE } from "./issueModel";
import { resolveIssueRef } from "./issueApi";
import { ISSUE_FILTERS_DEFAULT, type IssueFilters } from "./issueFilters";
import { DevPullRequestsTab } from "./DevPullRequestsTab";
import { DevReposTab } from "./DevReposTab";
import { useFeatures, useFeatureTabs } from "../../features";
import { layoutClass } from "../../components/Layout";

export default function DevelopmentView(props: SectionProps) {
  return <OmixorProvider><Development {...props} /></OmixorProvider>;
}

function Development({ focusId, clearFocus }: SectionProps) {
  const [rawTab, setRawTab] = useState<DevTab>("overview");
  // 分頁依系統設定;選的分頁關了就顯示第一個開著的。Repositories 的 key(development.repos)是選用模組 integrations 登記的:
  // 後端沒編進模組時清單沒有這個 key → 不顯示
  const flags = useFeatures();
  const tabs = useFeatureTabs(DEV_TABS.map((d) => d.id), rawTab, setRawTab, (t) => `development.${t}`);
  const tab = tabs.shown, setTab = tabs.select;
  const [selectedPR, setSelectedPR] = useState<number>();
  const [prRepoFilter, setPrRepoFilter] = useState<string>();
  const [highlightedItem, setHighlightedItem] = useState<string>();
  const [selectedIssue, setSelectedIssue] = useState<string>();
  const [selectedRepo, setSelectedRepo] = useState<string>();
  const [issueFilters, setIssueFiltersState] = useState<IssueFilters>(ISSUE_FILTERS_DEFAULT);
  const [prs, , prsReady] = useList<PullRequest>("pull_requests");
  const [issues, , issuesReady] = useList<Issue>("issues");
  const [work, , workReady] = useList<WorkItem>("work_items");
  const [repos, , reposReady] = useList<Repository>("repos");

  const model: DevModel = {
    tab, setTab, selectedPR, setSelectedPR, prRepoFilter, setPrRepoFilter, highlightedItem, setHighlightedItem, selectedIssue, setSelectedIssue,
    selectedRepo, setSelectedRepo,
    issueFilters, setIssueFilters: (patch) => setIssueFiltersState((f) => ({ ...f, ...patch })),
    showIssue: (id) => { setSelectedIssue(id); setTab("issues"); },
    showPR: (id) => { setSelectedPR(id); setTab("pulls"); },
    showRepoPRs: (repo) => { setPrRepoFilter(repo); setSelectedPR(undefined); setTab("pulls"); },
    showItem: (id) => { setHighlightedItem(id); setTab("board"); },
    showRepo: (id) => { if (id) setSelectedRepo(id); setTab("repos"); },
  };

  // 其他頁跳過來時:focusId 可能是 PR 編號(可帶 #)、Issue id、工作項目 id、Repositories 的 repo(「github:owner/name」「gitlab:專案」)、
  // "repos"(舊的 "github"、"gitlab" 分頁也改開 Repositories)或範例資料的 repo id(看那個 repo 的 Pull Requests)
  useEffect(() => {
    if (!focusId || !(prsReady && issuesReady && workReady && reposReady)) return;
    const trimmed = focusId.startsWith("#") ? focusId.slice(1) : focusId;
    const n = Number(trimmed);
    const issue = findIssue(issues, focusId); // id、目前的 key 都找得到
    if (Number.isInteger(n) && trimmed !== "" && prs.some((p) => p.id === n)) { setPrRepoFilter(undefined); model.showPR(n); }
    else if (issue) model.showIssue(issue.id);
    else if (focusId === "issues") setTab("issues");
    else if (focusId === "repos" || focusId === "github" || focusId === "gitlab") model.showRepo();
    else if (/^(github|gitlab):./.test(focusId)) model.showRepo(focusId);
    else if (work.some((w) => w.id === focusId)) model.showItem(focusId);
    else if (repos.some((r) => r.id === focusId)) { if (flags.enabled("development.pulls")) model.showRepoPRs(focusId); else model.showRepo(); }
    else if (UUID_RE.test(focusId) || KEY_RE.test(focusId)) {
      // 清單裡沒有:可能是舊的 key(搬過專案)或垃圾桶裡的;請後端解析。解析得到才選中,不管結果都要清掉 focus,免得一直卡著
      resolveIssueRef(focusId).then((r) => { if (!r.deleted) model.showIssue(r.id); }).catch(() => undefined).finally(clearFocus);
      return;
    }
    clearFocus(); // 什麼都對不上:清掉,不要一直留著
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, prsReady, issuesReady, workReady, reposReady]);

  return (
    <DevCtx.Provider value={model}>
      <div className={layoutClass.page}>
        <div className="tabbar">
          <Segmented value={tab} onChange={setTab} options={DEV_TABS.filter((d) => tabs.on(d.id)).map((d) => ({ value: d.id, label: <><SIcon name={d.icon} size={12} />{d.label}</> }))} />
        </div>
        <div className={layoutClass.page}>
          {tab === "overview" && <DevOverviewTab />}
          {tab === "board" && <DevBoardTab />}
          {tab === "issues" && <DevIssuesTab />}
          {tab === "pulls" && <DevPullRequestsTab />}
          {tab === "repos" && <DevReposTab />}
        </div>
      </div>
    </DevCtx.Provider>
  );
}
