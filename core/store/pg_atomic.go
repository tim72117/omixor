package store

import "context"

// ---- PG 的原子區塊(Atomic)與角色任務轉移:一個資料庫交易(Batch) ----

// Atomic fn 裡用 tx 做的寫入在同一個資料庫交易裡,全有或全無(見 Batch;已經在批次裡就沿用同一個交易)
func (p *PG) Atomic(fn func(tx Store) error) error {
	return p.Batch(func(tx *PG) error { return fn(tx) })
}

// TransferAgentTasks 把 o.From 名下的任務轉給 o.To(一個交易;規則見 transferAgentTasks)
func (p *PG) TransferAgentTasks(org string, o AgentTransfer) (res AgentTransferResult, err error) {
	err = p.Batch(func(tx *PG) error {
		// 先鎖住來源角色名下的任務列:之後讀到的狀態就是最新的,並發的領取、回報在這些列上排隊到轉移結束,
		// 不會在「讀到、寫回」之間把轉移蓋掉(M1)。多個實例之間不是靠程序內的 agents.mu 而是靠這個資料庫行鎖
		if _, e := tx.pool.Exec(context.Background(), `select 1 from omixor_agent_tasks where org = $1 and agent_id = $2 order by id for update`, org, o.From); e != nil {
			return e
		}
		var e error
		res, e = transferAgentTasks(tx, org, o)
		return e
	})
	return res, err
}
