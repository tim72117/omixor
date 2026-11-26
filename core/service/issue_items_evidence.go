package service

import (
	"encoding/json"
	"slices"
	"strings"

	"omixor-core/model"
)

// 子項完成證據的查核(ISS-44):讀取時計算,不存庫。把 evidence.commit 拿去對回報過的 commits(commits.go),其餘欄位一律是自述。
//
//	範圍:Issue 有標 repo 查那個 repo(沒前綴的 key 也算同一個;Issue 沒前綴時任何平台都算),沒標 repo 查整個組織
//	可見性:能看到子項的人才看得到結果(子項繼承 Issue);角色憑證不能讀 commits(commitCaller),只查它自己的子項
//	(子項的 TaskID = 憑證綁定的任務,也就是它能回報的那一個;done 之後 TaskID 還在),其他子項回 not_visible,避免變成探測管道
//	「回報過」是有人用 commits_report / commits sync 送過內容,不代表經過程式碼平台查證(model.Commit.Verified)

const (
	evidenceCommitKey     = "commit"
	evidenceUnverifiedKey = "unverified"
)

// evidenceFacts 從 evidence JSON 讀出查核要用的東西
type evidenceFacts struct {
	keys       []string // 全部欄位名(排序)
	hasCommit  bool
	sha        string // 合格的 sha 前綴(小寫);hasCommit 但這裡是空 = 格式不對
	unverified bool   // 有說明哪些沒驗證
}

func readEvidence(raw json.RawMessage) evidenceFacts {
	var f evidenceFacts
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return f
	}
	for k := range m {
		f.keys = append(f.keys, k)
	}
	slices.Sort(f.keys)
	commit := m[evidenceCommitKey]
	f.hasCommit = hasContent(commit)
	if s, ok := commit.(string); ok {
		if pre, ok := model.CommitSHAPrefix(s); ok {
			f.sha = pre
		}
	}
	f.unverified = hasContent(m[evidenceUnverifiedKey])
	return f
}

// hasContent JSON 值是不是有內容(null、false、0、空白字串、空陣列、空物件都不算)
func hasContent(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return slices.ContainsFunc(x, hasContent)
	case map[string]any:
		return len(x) > 0
	case bool:
		return x
	case float64:
		return x != 0
	}
	return false
}

// canCheckCommit 呼叫者能不能看這個子項的 commit 查核結果:人都可以;角色憑證只有綁定自己目前任務的子項
func canCheckCommit(p *model.Principal, it model.IssueItem) bool {
	if p == nil {
		return false
	}
	return p.Agent == nil || (it.TaskID != "" && strings.EqualFold(it.TaskID, p.Agent.TaskID))
}

// commitEvidence 一批 sha 的查詢結果
type commitEvidence struct {
	visible bool // 呼叫者能不能看這個子項的結果(canCheckCommit,逐項填)
	found   map[string]bool
	hasData bool
}

func (c commitEvidence) status(f evidenceFacts) string {
	switch {
	case !f.hasCommit:
		return ""
	case f.sha == "":
		return model.EvidenceInvalidSHA
	case !c.visible:
		return model.EvidenceNotVisible
	case c.found[f.sha]:
		return model.EvidenceConfirmed
	case !c.hasData:
		return model.EvidenceUnverifiable
	}
	return model.EvidenceNotFound
}

// judgeEvidence 一個子項的查核結果;還沒完成又沒有證據回 nil
func judgeEvidence(status string, f evidenceFacts, c commitEvidence) *model.EvidenceCheck {
	done := status == model.ItemDone
	if !done && len(f.keys) == 0 {
		return nil
	}
	out := &model.EvidenceCheck{Commit: c.status(f)}
	for _, k := range f.keys {
		if k != evidenceCommitKey || out.Commit != model.EvidenceConfirmed {
			out.SelfReported = append(out.SelfReported, k)
		}
	}
	// not_visible 不標:看不到結果的呼叫者,看到的結果不能因為 commit 存在與否而不同
	out.NeedsVerification = done && !f.unverified && out.Commit != model.EvidenceConfirmed && out.Commit != model.EvidenceNotVisible
	return out
}

// evidenceRepos Issue 的 repo 標記 → 要查的 commit repo key;空 = 整個組織
func evidenceRepos(issueRepo string) []string {
	key, ok := model.CommitRepoKey(issueRepo)
	if !ok {
		return nil
	}
	provider, path := model.CommitRepoSplit(key)
	if provider == "" {
		return model.CommitRepoCandidates(path)
	}
	return []string{key, path}
}

// checkEvidence 填 items(同一張 Issue、呼叫端確認過看得到)的 EvidenceCheck;commits 整批查一次
func (s *Service) checkEvidence(p *model.Principal, org string, issue *model.Issue, items []model.IssueItem) error {
	facts := make([]evidenceFacts, len(items))
	var shas []string
	visible := make([]bool, len(items))
	for k, it := range items {
		facts[k], visible[k] = readEvidence(it.Evidence), canCheckCommit(p, it)
		if visible[k] && facts[k].sha != "" {
			shas = append(shas, facts[k].sha)
		}
	}
	var c commitEvidence
	if len(shas) > 0 {
		slices.Sort(shas)
		var err error
		if c.found, c.hasData, err = s.Store.LookupReportedCommits(org, evidenceRepos(issue.Repo), slices.Compact(shas)); err != nil {
			return err
		}
	}
	for k := range items {
		c.visible = visible[k]
		items[k].EvidenceCheck = judgeEvidence(items[k].Status, facts[k], c)
	}
	return nil
}
