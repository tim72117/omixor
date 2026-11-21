package core

import (
	"omixor-core/backend"
	"omixor-core/service"
	"omixor-core/store"
)

// UseTestStores 沒有資料庫(OMIXOR_DATABASE_URL)時,本機模式的核心改用測試給的 store:每個資料夾 DataRoot 一份(get 的參數是 DataRoot())。
// 只給各測試套件的 TestMain 呼叫;正式程式不呼叫,本機模式沒有資料庫就回 errNoDB。
// get 會在持有 localCoreMu 時呼叫,裡面不可以再用 core 的核心。取得 store 的做法見 core/coretest(測試專用,不進正式程式)。
func UseTestStores(get func(dataRoot string) *store.PG) {
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	ConfigureLocalIdentity() // 測試的 store 在第一次用核心時才建(NewPG 之前先設好本機別名,同正式程式的啟動順序)
	newTestCore = func() *backend.Local { // 在 localCoreMu 裡呼叫
		st := get(DataRoot())
		if st == nil { // 測試程序正在結束(get 回 nil):留下的背景 goroutine 拿不到核心,當成沒有資料庫
			return nil
		}
		p, err := ensureLocalPrincipal(st) // 本機使用者與 actor(冪等)
		if err != nil {
			// 測試程序結束時 storetest.Main 會 drop 資料庫(強制中斷連線),測試留下的 5 秒計時器 goroutine 這時可能正在用;
			// 再問一次 get:回 nil 就是結束中的正常現象,不 panic(其他原因的失敗照樣 panic)
			if get(DataRoot()) == nil {
				return nil
			}
			panic("測試核心的本機使用者建立失敗:" + err.Error())
		}
		return &backend.Local{Svc: &service.Service{Store: st}, P: p, EnsureOrg: true}
	}
}
