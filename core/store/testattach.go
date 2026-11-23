package store

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDBPrefix core/storetest/testdb 替測試建的資料庫名稱前綴(omixor_t_<時間>_<pid>_<序號>);改那邊的命名時這裡要一起改
const testDBPrefix = "omixor_t_"

// attachRequiredTables 已遷移完成的資料庫一定有的表(結構指紋的最低門檻)
var attachRequiredTables = []string{"omixor_orgs", "ops_issues", "omixor_agents", "omixor_seq"}

// checkAttachDB 資料庫名稱要是測試資料庫,且必要的表都在;否則回錯誤(純函式,方便測)
func checkAttachDB(dbName string, tables map[string]bool) error {
	if !strings.HasPrefix(dbName, testDBPrefix) {
		return fmt.Errorf("AttachMigratedPG 只能連測試資料庫(名稱要以 %s 開頭),目前是 %q", testDBPrefix, dbName)
	}
	var missing []string
	for _, t := range attachRequiredTables {
		if !tables[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("資料庫還沒遷移完成(缺表 %s);AttachMigratedPG 不建表也不遷移", strings.Join(missing, "、"))
	}
	return nil
}

// AttachMigratedPG 連到一個「已經建好結構」的測試資料庫,不跑建表(NewPG 會跑,約 200ms)。
//
// 只給 core/storetest 用,正式程式(core、console、server)不得呼叫(有測試用 go/ast 掃描把關,見 testattach_test.go):
// 它繞過 NewPG 的建表與舊結構偵測。為了避免誤用,連線後會先檢查 current_database() 要是測試資料庫
// (名稱以 omixor_t_ 開頭),且必要的表都在(沒遷移完成的資料庫會回錯誤);不符就關掉連線池回錯誤。
// 正式程式請用 NewPG。
func AttachMigratedPG(url string) (*PG, error) {
	c, cancel := ctx()
	defer cancel()
	pool, err := pgxpool.New(c, url)
	if err != nil {
		return nil, fmt.Errorf("資料庫網址格式不對:%w", err)
	}
	fail := func(err error) (*PG, error) {
		pool.Close()
		return nil, err
	}
	if err := pool.Ping(c); err != nil {
		return fail(fmt.Errorf("連不到資料庫:%w", err))
	}
	var db string
	if err := pool.QueryRow(c, `select current_database()`).Scan(&db); err != nil {
		return fail(err)
	}
	tables := map[string]bool{}
	rows, err := pool.Query(c, `select tablename from pg_tables where schemaname = current_schema()`)
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return fail(err)
		}
		tables[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	if err := checkAttachDB(db, tables); err != nil {
		return fail(err)
	}
	p := newPG(pool)
	return p, nil
}
