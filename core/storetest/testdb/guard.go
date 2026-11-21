package testdb

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// 本檔與 fingerprint.go、sweep.go 不讀環境變數(config 守門只放行 testdb.go 讀);環境變數由 testdb.go 讀了再傳進來。

// AllowRemoteEnv 設 1:OMIXOR_TEST_DATABASE_URL 可以指到非本機、或名稱不像測試用的資料庫
// (testdb 會在那台伺服器上建 omixor_t_* / omixor_tpl_* 資料庫、強制中斷它們的連線、並 drop 過期的同名資料庫,別指到正式或共用的伺服器)
const AllowRemoteEnv = "OMIXOR_TEST_ALLOW_REMOTE"

// parseHostsDB 取出連線字串的主機清單與資料庫名稱(URL 或 key=value 兩種格式;不印也不回傳密碼)
func parseHostsDB(raw string) (hosts []string, db string, err error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, "", fmt.Errorf("連線網址格式不對")
		}
		db = strings.TrimPrefix(u.Path, "/")
		if h := u.Query().Get("host"); h != "" {
			hosts = strings.Split(h, ",")
		} else if u.Host != "" {
			for _, hp := range strings.Split(u.Host, ",") {
				if h, _, err := net.SplitHostPort(hp); err == nil {
					hp = h
				}
				hosts = append(hosts, strings.Trim(hp, "[]"))
			}
		}
		if q := u.Query().Get("dbname"); q != "" && db == "" {
			db = q
		}
		return hosts, db, nil
	}
	for _, f := range strings.Fields(raw) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "host":
			hosts = strings.Split(v, ",")
		case "dbname":
			db = v
		}
	}
	return hosts, db, nil
}

func isLocalHost(h string) bool {
	switch {
	case h == "", h == "localhost", strings.HasPrefix(h, "/"), strings.HasPrefix(h, "@"): // 空 = 預設(unix socket 或 localhost);/ 開頭 = socket 目錄
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// looksLikeTestDB 資料庫名稱像測試用:以 omixor_ 開頭或含 test
func looksLikeTestDB(db string) bool {
	db = strings.ToLower(db)
	return strings.HasPrefix(db, "omixor_") || strings.Contains(db, "test")
}

// checkTarget 判斷 OMIXOR_TEST_DATABASE_URL 能不能放行:主機全是本機、資料庫名稱像測試用就放行;
// 否則要 allowRemote(OMIXOR_TEST_ALLOW_REMOTE=1)。回傳的錯誤說明原因(不含密碼)
func checkTarget(raw string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	hosts, db, err := parseHostsDB(raw)
	if err != nil {
		return err
	}
	var why []string
	for _, h := range hosts {
		if !isLocalHost(h) {
			why = append(why, fmt.Sprintf("主機 %q 不是本機", h))
			break
		}
	}
	if !looksLikeTestDB(db) {
		why = append(why, fmt.Sprintf("資料庫名稱 %q 不像測試用(要以 omixor_ 開頭或含 test)", db))
	}
	if len(why) == 0 {
		return nil
	}
	return fmt.Errorf("拒絕使用 %s 指到的資料庫:%s。測試會在該伺服器上建立大量 omixor_t_* 資料庫、強制中斷它們的連線,並 drop 過期的同名資料庫,不要指到正式或共用的伺服器;"+
		"確定要用就設 %s=1", URLEnv, strings.Join(why, ";"), AllowRemoteEnv)
}
