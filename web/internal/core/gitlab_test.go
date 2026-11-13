package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GitLab:假的自架 GitLab(httptest,自簽憑證 gitlab.corp.test)

const glHost = "gitlab.corp.test"

// selfSigned 產生自簽憑證(自己就是 CA),回傳憑證與 PEM 檔路徑
func selfSigned(t *testing.T, hosts ...string) (tls.Certificate, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: hosts[0]},
		DNSNames: hosts, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kb, _ := x509.MarshalECPrivateKey(key)
	cert, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "corp-ca.pem")
	if err := os.WriteFile(path, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return cert, path
}

func fakeGitLab(r *http.Request, body []byte) (int, any) {
	if r.Host == "storage.corp.test" { // trace 轉址到物件儲存
		if r.Header.Get("Authorization") != "" {
			return 403, map[string]any{"message": "token 不該送到這裡"}
		}
		lines := []string{"section_start:1700000000:step_script\r\x1b[0K\x1b[32;1m$ go test ./...\x1b[0;m"}
		for i := 1; i <= 30; i++ {
			lines = append(lines, fmt.Sprintf("\x1b[31mline %d\x1b[0m", i))
		}
		lines = append(lines, "Downloading 10%\rDownloading 100%", "section_end:1700000001:step_script\r\x1b[0K\x1b[31;1mERROR: Job failed: exit code 1\x1b[0;m", "")
		return 200, RawBody(strings.Join(lines, "\n"))
	}
	if r.Host != glHost {
		return 404, map[string]any{"message": "wrong host " + r.Host}
	}
	path := r.URL.EscapedPath()
	q := r.URL.Query()
	switch path {
	case "/.well-known/openid-configuration":
		return 200, map[string]any{"issuer": "https://" + glHost, "authorization_endpoint": "https://" + glHost + "/oauth/authorize",
			"token_endpoint": "https://" + glHost + "/oauth/token"}
	case "/oauth/token":
		f := formOf(body)
		if f["grant_type"] == "authorization_code" && f["code"] == "gl-code" && f["code_verifier"] != "" && f["client_id"] == "app-1" {
			return 200, map[string]any{"access_token": "gl-oauth-at", "refresh_token": "gl-rt", "expires_in": 7200, "token_type": "Bearer"}
		}
		return 400, map[string]any{"error": "invalid_grant", "error_description": "bad code " + string(body)}
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer glpat-test" && auth != "Bearer gl-oauth-at" {
		return 401, map[string]any{"message": "401 Unauthorized"}
	}
	base := "/api/v4/projects/web%2Fshop"
	pipeline := func(id int, status string, detail bool) map[string]any {
		p := map[string]any{"id": id, "iid": id - 400, "project_id": 7, "sha": fmt.Sprintf("sha%d", id), "ref": "main", "status": status,
			"source": "push", "created_at": fmt.Sprintf("2026-11-10T1%d:00:00.123Z", id-500), "updated_at": "2026-11-10T12:00:00Z",
			"web_url": fmt.Sprintf("https://%s/web/shop/-/pipelines/%d", glHost, id)}
		if detail {
			p["user"] = map[string]any{"username": "tim", "name": "Tim"}
			p["started_at"] = "2026-11-10T10:00:00Z"
			p["finished_at"] = "2026-11-10T10:05:00Z"
			p["duration"] = 300
		}
		return p
	}
	switch path {
	case "/api/v4/user":
		return 200, map[string]any{"username": "tim", "name": "Tim", "email": "tim@corp.test"}
	case base:
		return 200, map[string]any{"id": 7, "name": "shop", "path_with_namespace": "web/shop", "default_branch": "main",
			"web_url": "https://" + glHost + "/web/shop", "visibility": "private", "last_activity_at": "2026-11-10T12:00:00.5Z"}
	case "/api/v4/projects/web%2Fsecret":
		return 404, map[string]any{"message": "404 Project Not Found"}
	case base + "/merge_requests":
		if q.Get("state") != "opened" {
			return 400, map[string]any{"error": "state"}
		}
		mr := func(iid int, draft bool) map[string]any {
			return map[string]any{"id": 100 + iid, "iid": iid, "title": fmt.Sprintf("MR %d", iid), "draft": draft,
				"author": map[string]any{"username": "amy", "name": "Amy"}, "source_branch": fmt.Sprintf("feat/%d", iid), "target_branch": "main",
				"labels": []any{"backend"}, "reviewers": []any{map[string]any{"username": "tim"}}, "sha": "abc",
				"updated_at": fmt.Sprintf("2026-11-10T0%d:00:00Z", iid), "web_url": fmt.Sprintf("https://%s/web/shop/-/merge_requests/%d", glHost, iid),
				"detailed_merge_status": "mergeable"}
		}
		// 分頁:每頁一筆,X-Next-Page
		if q.Get("page") == "" || q.Get("page") == "1" {
			return 200, WithHeaders{Header: map[string]string{"X-Next-Page": "2", "X-Total-Pages": "2"}, Body: []any{mr(1, false)}}
		}
		return 200, WithHeaders{Header: map[string]string{"X-Next-Page": ""}, Body: []any{mr(2, true)}}
	case base + "/merge_requests/1":
		return 200, map[string]any{"iid": 1, "title": "MR 1", "description": "## 改了什麼\n- 結帳", "author": map[string]any{"username": "amy"},
			"head_pipeline": map[string]any{"id": 501, "status": "failed", "web_url": "https://" + glHost + "/p/501"}, "changes_count": "2",
			"source_branch": "feat/1", "target_branch": "main"}
	case base + "/merge_requests/2":
		return 200, map[string]any{"iid": 2, "title": "MR 2", "draft": true}
	case base + "/merge_requests/1/approvals":
		return 200, map[string]any{"approved": false, "approvals_required": 2, "approvals_left": 1,
			"approved_by": []any{map[string]any{"user": map[string]any{"username": "lee"}}}}
	case base + "/merge_requests/2/approvals":
		return 404, map[string]any{"message": "404 Not found"}
	case base + "/merge_requests/1/diffs":
		return 200, []any{
			map[string]any{"old_path": "a.go", "new_path": "a.go", "diff": "@@ -1,2 +1,3 @@\n-old\n+new\n+more\n ctx\n"},
			map[string]any{"old_path": "b.go", "new_path": "b.go", "new_file": true, "diff": "@@ -0,0 +1 @@\n+hi\n"},
		}
	case base + "/pipelines":
		return 200, []any{pipeline(501, "failed", false), pipeline(500, "success", false)}
	case base + "/pipelines/501", base + "/pipelines/500":
		id := 501
		status := "failed"
		if strings.HasSuffix(path, "500") {
			id, status = 500, "success"
		}
		return 200, pipeline(id, status, true)
	case base + "/pipelines/501/jobs":
		job := func(id int, name, stage, status string) map[string]any {
			return map[string]any{"id": id, "name": name, "stage": stage, "status": status, "duration": 12.5,
				"web_url": fmt.Sprintf("https://%s/web/shop/-/jobs/%d", glHost, id), "allow_failure": false, "failure_reason": map[string]string{"failed": "script_failure"}[status]}
		}
		// 分頁:Link rel="next"(同一台主機才跟)
		if q.Get("page") == "2" {
			return 200, WithHeaders{Header: map[string]string{"Link": `<https://evil.test/api/v4/x?page=3>; rel="next"`}, Body: []any{job(900, "build", "build", "success")}}
		}
		next := "https://" + glHost + base + "/pipelines/501/jobs?page=2&per_page=100"
		return 200, WithHeaders{Header: map[string]string{"Link": "<" + next + `>; rel="next", <` + next + `>; rel="last"`}, Body: []any{job(901, "test", "test", "failed")}}
	case base + "/jobs/901/trace":
		return 302, Redirect("https://storage.corp.test/traces/901?sig=x")
	}
	return 404, map[string]any{"message": "404 Not Found " + path}
}

func glSetup(t *testing.T, ca string, insecure bool) (*App, *FakeNet) {
	a := connTestApp(t)
	cert, caPath := selfSigned(t, glHost, "storage.corp.test")
	f := NewFakeNetTLS(fakeGitLab, cert)
	t.Cleanup(f.Close)
	if ca == "auto" {
		ca = caPath
	}
	base := "https://" + glHost + "/"
	if _, err := GLSetServer(a, &base, &ca, &insecure); err != nil {
		t.Fatal(err)
	}
	return a, f
}

func TestGLNormalize(t *testing.T) {
	for in, want := range map[string]string{"web/shop": "web/shop", " https://gitlab.corp.test/web/sub/shop.git ": "web/sub/shop",
		"https://gitlab.corp.test/web/shop/-/merge_requests/3": "web/shop", "git@gitlab.corp.test:web/shop.git": "web/shop", "42": "42",
		"shop": "", "web/sh op": "", "0": "", "web/shop?x=1": ""} {
		got, ok := GLNormalizeProject(in, "https://gitlab.corp.test")
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v,要 %q", in, got, ok, want)
		}
	}
	for in, want := range map[string]string{"https://gitlab.corp.test/": "https://gitlab.corp.test", "https://g.test/gitlab/api/v4": "https://g.test/gitlab"} {
		if got, err := GLNormalizeBaseURL(in); err != nil || got != want {
			t.Errorf("%s → %s %v", in, got, err)
		}
	}
	if _, err := GLNormalizeBaseURL("gitlab.corp.test"); err == nil {
		t.Error("沒有 https:// 要擋")
	}
	if glProjectPath("web/sub/shop") != "/api/v4/projects/web%2Fsub%2Fshop" {
		t.Error(glProjectPath("web/sub/shop"))
	}
	if got := GLCleanTrace("section_start:1:x\r\x1b[0K\x1b[32;1m$ make\x1b[0;m\nA 1%\rA 100%\r\n"); got != "$ make\nA 100%\n" {
		t.Errorf("%q", got)
	}
}

func TestGLSelfSignedNeedsCA(t *testing.T) {
	a, f := glSetup(t, "", false)
	ConnSetBearer(a, "gitlab", "glpat-test")
	_ = GLAddProject(a, "web/shop")
	GLRefresh(a)
	a.Mu.Lock()
	snap := GLSnapshotNow(a)
	a.Mu.Unlock()
	if len(snap.Projects) != 0 || !strings.HasPrefix(snap.Errors["web/shop"], "憑證驗證失敗:伺服器的憑證不是受信任的 CA 簽的") {
		t.Fatalf("自簽憑證沒指定 CA 要失敗:%+v", snap)
	}
	if len(f.Requests()) != 0 {
		t.Fatal("TLS 失敗時伺服器不該收到請求")
	}
	// 指定 CA 檔:連得上
	_, caPath := selfSigned(t, "other.test") // 別的 CA:還是失敗
	if _, err := GLSetServer(a, nil, &caPath, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := GLGet(a, "/api/v4/user"); err == nil || !strings.Contains(err.Error(), "裡沒有簽這張憑證的 CA") {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "x.pem")
	_ = os.WriteFile(bad, []byte("not pem"), 0o644)
	if _, err := GLSetServer(a, nil, &bad, nil); err == nil || !strings.Contains(err.Error(), "沒有 PEM 格式的憑證") {
		t.Fatal(err)
	}
	missing := "/nope/ca.pem"
	if _, err := GLSetServer(a, nil, &missing, nil); err == nil || !strings.HasPrefix(err.Error(), "讀不到 CA 憑證檔 /nope/ca.pem") {
		t.Fatal(err)
	}
	// 不驗證憑證(測試環境)
	yes := true
	if _, err := GLSetServer(a, nil, nil, &yes); err != nil {
		t.Fatal(err)
	}
	if o, err := GLGet(a, "/api/v4/user"); err != nil || o.(map[string]any)["username"] != "tim" {
		t.Fatalf("%v %v", o, err)
	}
	// 只改憑證設定不會登出
	if vaultTokens("", "gitlab") == nil {
		t.Fatal("token 不該被清掉")
	}
}

func TestGLRefreshWithCAPaginationApprovalsAndPipelines(t *testing.T) {
	a, f := glSetup(t, "auto", false)
	ConnSetBearer(a, "gitlab", "glpat-test")
	ConnRefreshAccount(a, "gitlab")
	for _, p := range []string{"https://gitlab.corp.test/web/shop", "web/secret", "web/shop"} {
		if err := GLAddProject(a, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := GLAddProject(a, "shop"); err == nil {
		t.Fatal("格式錯誤要擋")
	}
	if !GLLoggedIn(a) {
		t.Fatal("要已登入")
	}
	GLRefresh(a)
	a.Mu.Lock()
	snap := GLSnapshotNow(a)
	settings := GLSettingsNow(a)
	st := connStateCopy(a, "gitlab")
	a.Mu.Unlock()
	if strings.Join(settings.Projects, ",") != "web/shop,web/secret" {
		t.Fatalf("重複的專案不加:%v", settings.Projects)
	}
	if derefStr(st.Account) != "tim@corp.test" {
		t.Fatalf("%+v", st)
	}
	if snap.Error != nil || len(snap.Projects) != 1 || snap.Projects[0].Path != "web/shop" || snap.Projects[0].ID != 7 {
		t.Fatalf("%+v", snap)
	}
	if snap.Errors["web/secret"] != "找不到(確認專案路徑或 id;私有專案要登入的帳號有權限)" {
		t.Fatal(snap.Errors)
	}
	if len(snap.MRs) != 2 {
		t.Fatalf("分頁要讀完:%+v", snap.MRs)
	}
	mr := snap.MRs[1] // 依更新時間新的在前:MR 2 在前
	if snap.MRs[0].IID != 2 || !snap.MRs[0].Draft || mr.IID != 1 || mr.Author != "amy" || mr.SourceBranch != "feat/1" ||
		mr.Pipeline == nil || mr.Pipeline.Status != "failed" || mr.Approval == nil || mr.Approval.Left != 1 || strings.Join(mr.Approval.ApprovedBy, ",") != "lee" {
		t.Fatalf("%+v %+v", mr, mr.Approval)
	}
	if snap.MRs[0].Approval != nil {
		t.Fatal("讀不到核准時不填")
	}
	if len(snap.Pipelines) != 2 || snap.Pipelines[0].ID != 501 || snap.Pipelines[0].User != "tim" || *snap.Pipelines[0].Duration != 300 ||
		GLSourceLabel(snap.Pipelines[0].Source) != "push" || GLStatusLabel(snap.Pipelines[0].Status) != "失敗" {
		t.Fatalf("%+v", snap.Pipelines)
	}
	// 專案路徑要編碼成 web%2Fshop;只送 GET;token 只送 gitlab.corp.test
	for _, r := range f.Requests() {
		u, _ := url.Parse(r.URL)
		if r.Method != "GET" || u.Host != glHost || strings.Contains(r.URL, "/projects/web/shop") {
			t.Fatalf("%s %s", r.Method, r.URL)
		}
	}
	// 網址與專案是組織的設定,存在核心;這台的 gitlab.json 只寫用哪一個連線(專案不寫進去)
	be, err := agentBackend()
	if err != nil {
		t.Fatal(err)
	}
	cs, err := be.GetGitLabSettings(DefaultOrgID)
	if err != nil || !cs.Saved || cs.BaseURL != "https://"+glHost || strings.Join(cs.Projects, ",") != "web/shop,web/secret" {
		t.Fatalf("核心的 GitLab 設定:%+v %v", cs, err)
	}
	b, _ := os.ReadFile(filepath.Join(DataRoot(), "orgs", DefaultOrgID, "gitlab.json"))
	var saved GLSettings
	if len(b) > 0 && (json.Unmarshal(b, &saved) != nil || len(saved.Projects) != 0 || saved.Connection != "gitlab") {
		t.Fatalf("%s", b)
	}
	// connections.json:caCert 有寫、insecure 是 false 時省略;舊格式(Swift)照樣讀得出來
	cb, _ := os.ReadFile(filepath.Join(DataRoot(), "orgs", DefaultOrgID, "connections.json"))
	var raw []map[string]any
	_ = json.Unmarshal(cb, &raw)
	for _, c := range raw {
		_, hasIns := c["insecure"]
		_, hasCA := c["caCert"]
		if hasIns || hasCA != (c["id"] == "gitlab") {
			t.Fatalf("可省略的欄位:%v", c)
		}
	}

	// 移除專案
	GLRemoveProject(a, "web/secret")
	a.Mu.Lock()
	_, still := GLSnapshotNow(a).Errors["web/secret"]
	a.Mu.Unlock()
	if still {
		t.Fatal("移除專案要清掉錯誤")
	}
}

func TestGLJobsTraceAndMRDetail(t *testing.T) {
	a, f := glSetup(t, "auto", false)
	ConnSetBearer(a, "gitlab", "glpat-test")
	jobs, stages, err := GLJobs(a, "web/shop", 501)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != 900 || jobs[1].Stage != "test" || strings.Join(stages, ",") != "build,test" || jobs[1].FailureReason != "script_failure" {
		t.Fatalf("Link 分頁要讀完、依建立順序:%+v %v", jobs, stages)
	}
	for _, r := range f.Requests() {
		if strings.Contains(r.URL, "evil.test") {
			t.Fatal("別的主機的下一頁不能跟")
		}
	}
	tail, err := GLJobTraceTail(a, "web/shop", 901, 10)
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(tail, "\n")
	if len(ls) != 10 || ls[0] != "line 23" || ls[8] != "Downloading 100%" || ls[9] != "ERROR: Job failed: exit code 1" || strings.Contains(tail, "\x1b") {
		t.Fatalf("%q", tail)
	}
	if r, _ := f.Find("storage.corp.test"); r.Auth != "" {
		t.Fatal("trace 轉址到別的主機不能帶 token")
	}
	d, err := GLMRDetailGet(a, "web/shop", 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Description != "## 改了什麼\n- 結帳" || len(d.Files) != 2 || d.Files[1].Status != "added" || d.Additions != 3 || d.Deletions != 1 ||
		d.ChangesCount != "2" || d.MR.Approval.Required != 2 || d.MR.Pipeline.ID != 501 {
		t.Fatalf("%+v", d)
	}
	// Actions
	out, err := Action(a, "gitlab_jobs", map[string]any{"project": "web/shop", "pipeline": 501.0})
	if err != nil || len(out.(map[string]any)["jobs"].([]any)) != 2 {
		t.Fatalf("%v %v", out, err)
	}
	out, err = Action(a, "gitlab_job_log", map[string]any{"project": "web/shop", "job": "901", "lines": 3.0})
	if err != nil || !strings.HasSuffix(out.(map[string]any)["tail"].(string), "exit code 1") || len(strings.Split(out.(map[string]any)["text"].(string), "\n")) != 10 {
		t.Fatalf("lines 最少 10:%v %v", out, err)
	}
	if _, err := Action(a, "gitlab_mr", map[string]any{"project": "web/shop"}); err == nil || err.Error() != "要給 iid" {
		t.Fatal(err)
	}
}

func TestGL401AndNotConfigured(t *testing.T) {
	a := connTestApp(t)
	GLRefresh(a)
	a.Mu.Lock()
	e := GLSnapshotNow(a).Error
	a.Mu.Unlock()
	if e == nil || *e != "還沒設定 GitLab 連線(開發進度 › GitLab)" {
		t.Fatal(e)
	}
	if _, err := GLEnsureConnection(a, ""); err != nil {
		t.Fatal(err)
	}
	GLRefresh(a)
	a.Mu.Lock()
	e = GLSnapshotNow(a).Error
	a.Mu.Unlock()
	if *e != "還沒設定 GitLab 網址" {
		t.Fatal(*e)
	}

	a, _ = glSetup(t, "auto", false)
	_ = GLAddProject(a, "web/shop")
	GLRefresh(a)
	a.Mu.Lock()
	e = GLSnapshotNow(a).Error
	a.Mu.Unlock()
	if *e != "還沒登入 GitLab" {
		t.Fatal(*e)
	}
	ConnSetBearer(a, "gitlab", "expired")
	GLRefresh(a)
	a.Mu.Lock()
	snap := GLSnapshotNow(a)
	a.Mu.Unlock()
	if snap.Error == nil || *snap.Error != "登入已失效,請重新登入" || snap.Busy {
		t.Fatalf("%+v", snap)
	}
	// 網址換了:舊的 token 作廢
	ConnSetBearer(a, "gitlab", "glpat-test")
	other := "https://gitlab2.corp.test"
	c, err := GLSetServer(a, &other, nil, nil)
	if err != nil || c.OAuth.Issuer != other || vaultTokens("", "gitlab") != nil {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestGLOAuthLoginWithDiscoveryPKCEAndCA(t *testing.T) {
	a, f := glSetup(t, "auto", false)
	a.Mu.Lock()
	c, _ := GLConnection(a)
	a.Mu.Unlock()
	c.OAuth.ClientID = "app-1"
	ConnUpsert(a, c)
	var opened string
	browserApproves(t, f, "gl-code", "state", nil, &opened)
	ConnLogin(a, "gitlab")
	a.Mu.Lock()
	st := connStateCopy(a, "gitlab")
	got, _ := GLConnection(a)
	a.Mu.Unlock()
	if st.Error != nil {
		t.Fatalf("登入失敗:%s", *st.Error)
	}
	if derefStr(st.Account) != "tim@corp.test" || got.OAuth.TokenEndpoint != "https://"+glHost+"/oauth/token" {
		t.Fatalf("%+v %+v", st, got.OAuth)
	}
	u, _ := url.Parse(opened)
	q := u.Query()
	if u.Host != glHost || u.Path != "/oauth/authorize" || q.Get("scope") != "read_api read_user openid" || q.Get("code_challenge_method") != "S256" ||
		q.Get("client_id") != "app-1" || !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatal(opened)
	}
	if tk := vaultTokens("", "gitlab"); tk == nil || tk.AccessToken != "gl-oauth-at" {
		t.Fatal(tk)
	}
	if r, _ := f.Find("/.well-known/openid-configuration"); r.URL != "https://"+glHost+"/.well-known/openid-configuration" {
		t.Fatal(r.URL)
	}
}

func TestGLOrgsAndView(t *testing.T) {
	a, _ := glSetup(t, "auto", false)
	ConnSetBearer(a, "gitlab", "glpat-test")
	_ = GLAddProject(a, "web/shop")
	v, _ := View(a, "gitlab")
	m := v.(map[string]any)
	if m["loggedIn"] != true || m["configured"] != true || m["baseURL"] != "https://"+glHost || m["tokenURL"] == "" ||
		m["settings"].(map[string]any)["projects"].([]any)[0] != "web/shop" {
		t.Fatalf("%v", m)
	}
	projects := func() string {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return strings.Join(GLSettingsNow(a).Projects, ",")
	}
	a.Mu.Lock()
	blank := a.CreateOrg("Side", "", "", "blank")
	cp := a.CreateOrg("Copy", "", "", "copyCurrent")
	a.SwitchOrg(blank.ID)
	a.Mu.Unlock()
	if projects() != "" {
		t.Fatal(projects())
	}
	a.Mu.Lock()
	a.SwitchOrg(cp.ID)
	gc, ok := GLConnection(a)
	a.Mu.Unlock()
	if projects() != "web/shop" || !ok || gc.CACert == "" {
		t.Fatalf("copyCurrent 要複製專案與連線(含 CA):%s %+v", projects(), gc)
	}
}
