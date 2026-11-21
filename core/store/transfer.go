package store

import (
	"crypto/rand"
	"fmt"
	"time"

	"omixor-core/model"
)

// ---- 角色任務轉移 ----
//
// 刪角色(omixor_agent_tasks.agent_id 對 omixor_agents 是 restrict)前,名下的任務要先轉給別的角色。
// 轉移的邏輯只有一份(transferAgentTasks),PG 的 TransferAgentTasks 在「原子區塊」(Atomic)裡呼叫它;
// PG 的原子區塊是一個資料庫交易(Batch)。

// AgentTransfer 轉移的參數
type AgentTransfer struct {
	From, To string
	// OpenOnly true:只轉 queued、blocked(進行中、已完成、已取消的留在原角色);false:全部都轉(進行中的先退回待領取)
	OpenOnly bool
	// At 轉移時間(任務的 updatedAt 與備註的時間)
	At time.Time
	// NoteAuthor、NoteText 每筆被轉移的任務加的備註(NoteText 空 = 不加備註)
	NoteAuthor, NoteText string
}

// AgentTransferResult 轉移的結果
type AgentTransferResult struct {
	// ByStatus 被轉移的任務,依「轉移前」的狀態分的筆數
	ByStatus map[string]int `json:"byStatus"`
	// Total 被轉移的總筆數
	Total int `json:"total"`
	// Released 其中進行中被退回待領取(並作廢憑證)的筆數
	Released int `json:"released"`
}

// TaskCounts 角色名下各狀態的任務數(依狀態分,0 的不在裡面)與總數
func TaskCounts(tasks []model.AgentTask, agentID string) (map[string]int, int) {
	by := map[string]int{}
	n := 0
	for _, t := range tasks {
		if t.AgentID == agentID {
			by[t.Status]++
			n++
		}
	}
	return by, n
}

func newNoteID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// transferAgentTasks 在 st 上做轉移(呼叫端負責放在原子區塊裡)。不檢查角色是否存在(外鍵與 service 負責)
func transferAgentTasks(st Store, org string, o AgentTransfer) (AgentTransferResult, error) {
	res := AgentTransferResult{ByStatus: map[string]int{}}
	if o.From == "" || o.To == "" || o.From == o.To {
		return res, fmt.Errorf("轉移的來源與目標角色要不同、不能空白")
	}
	tasks, err := st.ListAgentTasks(org)
	if err != nil {
		return res, err
	}
	at := model.Time{Time: o.At.UTC().Truncate(time.Second)}
	for _, t := range tasks {
		if t.AgentID != o.From {
			continue
		}
		if o.OpenOnly && t.Status != "queued" && t.Status != "blocked" {
			continue
		}
		res.ByStatus[t.Status]++
		res.Total++
		x := t.Clone()
		if x.Status == "inProgress" { // 已被領走:退回待領取,鎖與領取者清掉,憑證作廢
			x.Status, x.LockedUntil, x.ClaimedBy = "queued", nil, ""
			res.Released++
		}
		x.AgentID = o.To
		x.UpdatedAt = at
		if o.NoteText != "" {
			x.Notes = append(x.Notes, model.AgentTaskNote{ID: newNoteID(), Author: o.NoteAuthor, Text: o.NoteText, At: at})
		}
		if err := st.UpdateAgentTask(org, x); err != nil {
			return res, fmt.Errorf("%s:%w", x.ID, err)
		}
		if t.Status != "done" && t.Status != "canceled" { // 還沒結束的任務,舊角色的憑證不能再用
			if err := st.DeleteAgentCredentials(org, x.ID, ""); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}
