package store

import (
	"errors"
	"strings"
)

// RenameIdentity 過去把資料裡的身分字串 old 改成 new(console 本機模式啟動時把舊的 ops-web 改成 omixor-console)。
// 「誰」欄位現在存的是 actor id(ISS-36 C-2 共用地基),改身分字串沒有意義:舊字串在轉換成 actor id 時已經由寬鬆解析器對到同一個 actor
// (別名表 omixor_actor_aliases,內建別名 omixor-console、ops-web),要改名是改 actor 的 handle(UpdateActorHandle)。
// 保留這個方法只為了 console 的 migrateLocalIdentity 還在呼叫,永遠不改任何資料、回傳 0(DOC-13 §3.6:移除 RenameIdentity 屬 C-2 後續清理,
// 連同 console 的 migrateLocalIdentity 一起刪)。
func (p *PG) RenameIdentity(old, new string) (int64, error) {
	old, new = strings.TrimSpace(old), strings.TrimSpace(new)
	if old == "" || new == "" || old == new {
		return 0, errors.New("身分名稱不能空白,也不能相同")
	}
	return 0, nil
}
