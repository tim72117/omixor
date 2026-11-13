package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 大量頻道訊息的匯入資料
func bigChannels(n int) *AppChannels {
	at := model.Now()
	c := &AppChannels{Channels: []model.Channel{{ID: "CH-1", Name: "一般", Members: []string{"user:tim"}, CreatedBy: "user:tim", CreatedAt: at, UpdatedAt: at}}}
	for i := 1; i <= n; i++ {
		c.Messages = append(c.Messages, model.ChannelMessage{ID: fmt.Sprintf("M-%d", i), ChannelID: "CH-1", Author: "user:tim", Text: "m", At: at})
	}
	c.Reads = []model.ChannelRead{{ChannelID: "CH-1", Member: "user:tim", LastRead: n, At: at}}
	return c
}

func countMessages(t *testing.T, l *Local, org string) int {
	t.Helper()
	chs, err := l.Store.ListChannels(org)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range chs {
		ms, _, err := l.Store.ListChannelMessages(org, c.ID, model.MessageQuery{Limit: 100000})
		if err != nil {
			t.Fatal(err)
		}
		n += len(ms)
	}
	return n
}

// 匯入做到一半出錯(等同被砍斷):這一類什麼都沒寫進磁碟,做完的類別有紀錄;重送後完整匯入
func TestLocalImportInterruptedRetry(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	at := model.Now()
	in := AppImport{
		Issues:   &[]model.Issue{{ID: "ISS-1", Title: "a", CreatedAt: at, UpdatedAt: at}},
		Channels: bigChannels(3000),
	}
	importFault = func(section string, n int) error {
		if section == "channels" && n == 1326 {
			return errors.New("模擬中斷")
		}
		return nil
	}
	defer func() { importFault = nil }()
	res, err := l.Import("default", in)
	if err != nil || res["issues"].Status != "done" || res["channels"].Status != "error" {
		t.Fatalf("%v %+v", err, res)
	}
	// 重開(等同重啟):磁碟上的頻道是全無,Issue 已記錄
	l2, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "2", User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if chs, _ := l2.Store.ListChannels("default"); len(chs) != 0 || countMessages(t, l2, "default") != 0 {
		t.Fatal("中斷的那一類不能留下一部分")
	}
	if lg := l2.readImportLog(); lg.Orgs["default"]["issues"].IsZero() || !lg.Orgs["default"]["channels"].IsZero() {
		t.Fatalf("匯入紀錄不對:%+v", lg)
	}
	importFault = nil
	res, err = l2.Import("default", in)
	if err != nil || res["issues"].Status != "skipped" || res["channels"].Status != "done" {
		t.Fatalf("重送要接著做:%v %+v", err, res)
	}
	if n := countMessages(t, l2, "default"); n != 3000 {
		t.Fatalf("要 3000 則,有 %d", n)
	}
	l3, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "3", User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if n := countMessages(t, l3, "default"); n != 3000 {
		t.Fatalf("重開後要 3000 則,有 %d", n)
	}
	if list, _ := filepath.Glob(filepath.Join(dir, ".store.json.tmp-*")); len(list) != 0 {
		t.Fatal("不能留下暫存檔", list)
	}
}

// 匯入紀錄遺失或損毀後重送:守護服務已有的資料不被 App 的舊資料覆蓋(組織鏈不整份取代、Issue 不回 409)
func TestLocalImportLogLost(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	h := l.Handler()
	at := model.Now()
	in := AppImport{
		Issues: &[]model.Issue{{ID: "ISS-1", Title: "a", CreatedAt: at, UpdatedAt: at}},
		Agents: &service.AgentImport{Agents: []model.Agent{{ID: "dev", Name: "工程師", Active: true}, {ID: "pm", Name: "PM", Active: true}}},
		Chain:  &model.OrgChain{Edges: []model.ChainEdge{{From: "user:tim", To: "agent:dev"}}},
	}
	if code, out := localReq(t, h, "POST", "/local/import/default", testTok, in); code != 200 {
		t.Fatal(code, out)
	}
	// 之後在守護服務改了組織鏈
	if code, out := localReq(t, h, "POST", "/api/orgs/default/chain", testTok, map[string]any{"edges": []map[string]any{{"from": "user:tim", "to": "agent:pm"}}}); code != 200 {
		t.Fatal(code, out)
	}
	for _, broken := range []bool{false, true} {
		if broken {
			_ = os.WriteFile(l.importLogPath(), []byte("{壞掉"), 0o600)
		} else {
			_ = os.Remove(l.importLogPath())
		}
		code, out := localReq(t, h, "POST", "/local/import/default", testTok, in)
		if code != 200 {
			t.Fatal(broken, code, out)
		}
		res := out["result"].(map[string]any)
		for _, k := range []string{"issues", "agents", "chain"} {
			if sec := res[k].(map[string]any); sec["status"] != "existing" || sec["note"] == "" {
				t.Fatal(broken, k, mustJSON(out))
			}
		}
		code, out = localReq(t, h, "GET", "/api/orgs/default/chain", testTok, nil)
		if code != 200 || !strings.Contains(mustJSON(out), `"to":"agent:pm"`) || strings.Contains(mustJSON(out), `"to":"agent:dev"`) {
			t.Fatal("組織鏈不能被 App 的舊版蓋回去", broken, mustJSON(out))
		}
		// 紀錄補寫了:再送一次是 skipped
		_, out = localReq(t, h, "POST", "/local/import/default", testTok, in)
		if !strings.Contains(mustJSON(out), `"skipped"`) || strings.Contains(mustJSON(out), `"existing"`) {
			t.Fatal("要補寫紀錄", mustJSON(out))
		}
	}
}

// DELETE /local/orgs/{org}:刪資料與匯入紀錄;同名重建是空的,可以再匯入;預設組織 400、找不到 404、沒登入 401
func TestLocalDeleteOrg(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	h := l.Handler()
	orgs := map[string]any{"orgs": []map[string]any{{"id": "acme", "name": "Acme"}}}
	if code, out := localReq(t, h, "POST", "/local/orgs", testTok, orgs); code != 200 {
		t.Fatal(code, out)
	}
	at := model.Now()
	in := AppImport{Issues: &[]model.Issue{{ID: "ISS-1", Title: "舊的", CreatedAt: at, UpdatedAt: at}}, Channels: bigChannels(3)}
	if code, out := localReq(t, h, "POST", "/local/import/acme", testTok, in); code != 200 {
		t.Fatal(code, out)
	}
	if code, _ := localReq(t, h, "DELETE", "/local/orgs/acme", "", nil); code != 401 {
		t.Fatal("沒登入要 401", code)
	}
	code, out := localReq(t, h, "DELETE", "/local/orgs/acme", testTok, nil)
	if code != 200 || out["deleted"] != true {
		t.Fatal(code, out)
	}
	if code, _ := localReq(t, h, "DELETE", "/local/orgs/acme", testTok, nil); code != 404 {
		t.Fatal("再刪要 404", code)
	}
	if _, out := localReq(t, h, "GET", "/local/import", testTok, nil); strings.Contains(mustJSON(out), "acme") {
		t.Fatal("匯入紀錄要刪掉", mustJSON(out))
	}
	// 同名重建:舊資料不會回來,可以重新匯入
	if code, out := localReq(t, h, "POST", "/local/orgs", testTok, orgs); code != 200 {
		t.Fatal(code, out)
	}
	if code, out := localReq(t, h, "GET", "/api/orgs/acme/issues", testTok, nil); code != 200 || strings.Contains(mustJSON(out), "ISS-1") {
		t.Fatal("舊 Issue 不能回來", code, mustJSON(out))
	}
	if chs, _ := l.Store.ListChannels("acme"); len(chs) != 0 {
		t.Fatal("舊頻道不能回來")
	}
	in2 := AppImport{Issues: &[]model.Issue{{ID: "ISS-7", Title: "新的", CreatedAt: at, UpdatedAt: at}}}
	code, out = localReq(t, h, "POST", "/local/import/acme", testTok, in2)
	if code != 200 || !strings.Contains(mustJSON(out), `"done"`) {
		t.Fatal(code, mustJSON(out))
	}
	// 預設組織不能刪
	code, out = localReq(t, h, "DELETE", "/local/orgs/default", testTok, nil)
	if code != 400 || !strings.Contains(mustJSON(out), "預設組織") {
		t.Fatal(code, out)
	}
	// 重開後刪除仍有效
	l2, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "2", User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if code, out := localReq(t, l2.Handler(), "GET", "/api/orgs/acme/issues", testTok+"2", nil); code != 200 || strings.Contains(mustJSON(out), "ISS-1") || !strings.Contains(mustJSON(out), "ISS-7") {
		t.Fatal(code, mustJSON(out))
	}
	// 最後一個組織:預設組織換成 acme 後,只剩 acme 時不能刪
	l3, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "3", User: "tim", Org: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	l3.Cfg.Org = "zzz" // 讓「預設組織」的規則不擋,只看「最後一個」
	if code, out := localReq(t, l3.Handler(), "DELETE", "/local/orgs/default", testTok+"3", nil); code != 200 {
		t.Fatal(code, out)
	}
	code, out = localReq(t, l3.Handler(), "DELETE", "/local/orgs/acme", testTok+"3", nil)
	if code != 400 || !strings.Contains(mustJSON(out), "最後一個") {
		t.Fatal(code, out)
	}
}

// 撤銷的獨立模式 token 重啟後不能復活;credentials.json 換成新的一把
func TestStandaloneRevokedSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenStandalone(LocalConfig{DataDir: dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	c1 := ReadStandaloneCred(dir)
	if code, out := localReq(t, l.Handler(), "POST", "/api/admin/tokens/"+c1.TokenID+"/revoke", c1.Token, nil); code != 200 {
		t.Fatal(code, out)
	}
	if code, _ := localReq(t, l.Handler(), "GET", "/api/orgs", c1.Token, nil); code != 401 {
		t.Fatal("撤銷後要 401", code)
	}
	l2, err := OpenStandalone(LocalConfig{DataDir: dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	c2 := ReadStandaloneCred(dir)
	if c2.Token == c1.Token || c2.TokenID == c1.TokenID || !validLocalToken(c2.Token) {
		t.Fatalf("要換一把新的:%+v", c2)
	}
	if code, _ := localReq(t, l2.Handler(), "GET", "/api/orgs", c1.Token, nil); code != 401 {
		t.Fatal("撤銷的 token 重啟後不能復活", code)
	}
	if code, _ := localReq(t, l2.Handler(), "GET", "/api/orgs", c2.Token, nil); code != 200 {
		t.Fatal(code)
	}
}

// 換本機使用者:舊使用者在同裝置名稱下的 token 作廢;rotate 作廢所有獨立模式 token;同雜湊不會對到兩個使用者
func TestLocalUserSwitchAndRotate(t *testing.T) {
	dir := t.TempDir()
	// App 模式換使用者
	if _, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok, User: "tim"}); err != nil {
		t.Fatal(err)
	}
	lb, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "b", User: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := localReq(t, lb.Handler(), "GET", "/api/orgs", testTok, nil); code != 401 {
		t.Fatal("換使用者後 tim 的 App token 要作廢", code)
	}
	// 獨立模式換使用者
	if _, err := OpenStandalone(LocalConfig{DataDir: dir, User: "tim"}, false); err != nil {
		t.Fatal(err)
	}
	c1 := ReadStandaloneCred(dir)
	ls, err := OpenStandalone(LocalConfig{DataDir: dir, User: "bob"}, false)
	if err != nil {
		t.Fatal(err)
	}
	c2 := ReadStandaloneCred(dir)
	if c2.Token == c1.Token || c2.Email != "bob" {
		t.Fatalf("換使用者要換 token:%+v", c2)
	}
	h := ls.Handler()
	if code, _ := localReq(t, h, "GET", "/api/orgs", c1.Token, nil); code != 401 {
		t.Fatal("tim 的獨立模式 token 要作廢", code)
	}
	if code, out := localReq(t, h, "GET", "/api/auth/me", c2.Token, nil); code != 200 || !strings.Contains(mustJSON(out), `"bob"`) {
		t.Fatal(code, out)
	}
	// rotate:這台發過的所有獨立模式 token(不管哪個使用者)都作廢
	alice := model.User{ID: "u_alice", Email: "alice", Role: "admin", CreatedAt: model.Now()}
	_ = ls.Store.SaveUser(alice)
	aliceTok := testTok + "alice"
	_ = ls.Store.CreateToken(model.Token{ID: "t_alice", UserID: alice.ID, Device: StandaloneDevice, CreatedAt: model.Now(), Hash: service.HashToken(aliceTok)})
	lr, err := OpenStandalone(LocalConfig{DataDir: dir, User: "bob"}, true)
	if err != nil {
		t.Fatal(err)
	}
	c3 := ReadStandaloneCred(dir)
	for _, tok := range []string{c2.Token, aliceTok} {
		if code, _ := localReq(t, lr.Handler(), "GET", "/api/orgs", tok, nil); code != 401 {
			t.Fatal("rotate 後要作廢", code)
		}
	}
	if code, _ := localReq(t, lr.Handler(), "GET", "/api/orgs", c3.Token, nil); code != 200 {
		t.Fatal(code)
	}
	// 同一個雜湊:別的使用者(別的裝置名稱)剛好有同一把的有效紀錄 → 作廢,只對到這次的使用者
	shared := testTok + "shared"
	_ = lr.Store.CreateToken(model.Token{ID: "t_shared", UserID: alice.ID, Device: "cli", CreatedAt: model.Now(), Hash: service.HashToken(shared)})
	lt, err := OpenLocal(LocalConfig{DataDir: dir, Token: shared, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	toks, _ := lt.Store.ListTokens(model.Now().Time)
	n := 0
	for _, tk := range toks {
		if string(tk.Hash) == string(service.HashToken(shared)) {
			n++
		}
	}
	if n != 1 {
		t.Fatal("同一個雜湊只能有一筆有效紀錄", n)
	}
	if code, out := localReq(t, lt.Handler(), "GET", "/api/auth/me", shared, nil); code != 200 || !strings.Contains(mustJSON(out), `"tim"`) {
		t.Fatal(code, out)
	}
}
