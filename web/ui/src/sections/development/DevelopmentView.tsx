// 開發進度:概覽 / Sprint 看板 / Issue / Pull Requests / Repositories / GitHub / GitLab(GitLab 只有 Web 版;Swift 的 DevelopmentView.swift)
import { useEffect, useState } from "react";
import { OpsProvider, Seg, SIcon, useList, type PullRequest, type Repository, type WorkItem } from "../../components/opsKit";
import type { SectionProps } from "..";
import { DEV_TABS, DevCtx, type DevModel, type DevTab } from "./devShared";
import { DevOverviewTab } from "./DevOverviewTab";
import { DevBoardTab } from "./DevBoardTab";
import { DevIssuesTab, type OpsIssue } from "./DevIssuesTab";
import { DevPullRequestsTab } from "./DevPullRequestsTab";
import { DevReposTab } from "./DevReposTab";
import { DevGitHubTab } from "./DevGitHubTab";
import { GitLabTab } from "../../gitlab/GitLabTab";

export default function DevelopmentView(props: SectionProps) {
  return <OpsProvider><Development {...props} /></OpsProvider>;
}

function Development({ focusId, clearFocus }: SectionProps) {
  const [tab, setTab] = useState<DevTab>("overview");
  const [selectedPR, setSelectedPR] = useState<number>();
  const [prRepoFilter, setPrRepoFilter] = useState<string>();
  const [highlightedItem, setHighlightedItem] = useState<string>();
  const [selectedIssue, setSelectedIssue] = useState<string>();
  const [prs, , prsReady] = useList<PullRequest>("pull_requests");
  const [issues, , issuesReady] = useList<OpsIssue>("issues");
  const [work, , workReady] = useList<WorkItem>("work_items");
  const [repos, , reposReady] = useList<Repository>("repos");

  const model: DevModel = {
    tab, setTab, selectedPR, setSelectedPR, prRepoFilter, setPrRepoFilter, highlightedItem, setHighlightedItem, selectedIssue, setSelectedIssue,
    showIssue: (id) => { setSelectedIssue(id); setTab("issues"); },
    showPR: (id) => { setSelectedPR(id); setTab("pulls"); },
    showRepoPRs: (repo) => { setPrRepoFilter(repo); setSelectedPR(undefined); setTab("pulls"); },
    showItem: (id) => { setHighlightedItem(id); setTab("board"); },
  };

  // 其他頁跳過來時:focusId 可能是 PR 編號(可帶 #)、Issue id、工作項目 id 或 repo id
  useEffect(() => {
    if (!focusId || !(prsReady && issuesReady && workReady && reposReady)) return;
    const trimmed = focusId.startsWith("#") ? focusId.slice(1) : focusId;
    const n = Number(trimmed);
    const issue = issues.find((i) => i.id.toLowerCase() === focusId.toLowerCase());
    if (Number.isInteger(n) && trimmed !== "" && prs.some((p) => p.id === n)) { setPrRepoFilter(undefined); model.showPR(n); }
    else if (issue) model.showIssue(issue.id);
    else if (focusId === "issues") setTab("issues");
    else if (focusId === "github") setTab("github");
    else if (focusId === "gitlab") setTab("gitlab");
    else if (work.some((w) => w.id === focusId)) model.showItem(focusId);
    else if (repos.some((r) => r.id === focusId)) setTab("repos");
    else return;
    clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, prsReady, issuesReady, workReady, reposReady]);

  return (
    <DevCtx.Provider value={model}>
      <div className="ops-page">
        <div className="ops-tabbar">
          <Seg value={tab} onChange={setTab} options={DEV_TABS.map((d) => ({ value: d.id, label: <><SIcon name={d.icon} size={12} />{d.label}</> }))} />
        </div>
        <div className="ops-page">
          {tab === "overview" && <DevOverviewTab />}
          {tab === "board" && <DevBoardTab />}
          {tab === "issues" && <DevIssuesTab />}
          {tab === "pulls" && <DevPullRequestsTab />}
          {tab === "repos" && <DevReposTab />}
          {tab === "github" && <DevGitHubTab />}
          {tab === "gitlab" && <GitLabTab />}
        </div>
      </div>
    </DevCtx.Provider>
  );
}
