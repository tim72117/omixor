package server

// 本機守護服務(omixor-local)內建的 PostgreSQL:用 github.com/fergusstrange/embedded-postgres 起一個只給自己用的 PostgreSQL 17。
//
//   - 第一次啟動要從網路下載 PostgreSQL 二進位檔(各平台約 10–60 MB,視平台而定),存在使用者的快取
//     (預設 ~/.embedded-postgres-go,Windows 是 %USERPROFILE%\.embedded-postgres-go),之後離線也能啟動
//   - 資料放 <資料夾>/pgdata(PostgreSQL 的資料目錄,會一直保留);<資料夾>/pgruntime 是每次啟動解壓出來的程式,
//     函式庫每次啟動都會清掉重建,不要放別的東西;<資料夾>/pgpassword 是第一次產生的隨機密碼(0600,Windows 靠使用者資料夾的 ACL),
//     資料目錄第一次初始化時就用它,之後要連線都要它
//   - 只聽 127.0.0.1;埠每次啟動隨機挑一個沒被佔用的(連線網址每次不同,只給這個程式自己用)
//   - 乾淨關閉:Stop 呼叫 pg_ctl stop -m fast;上次沒關乾淨(程式被砍掉)留下的 postmaster.pid:PostgreSQL 自己會判斷是不是過期的;
//     如果上次的 postgres 還活著(孤兒程序),啟動前先用 pg_ctl stop 把它關掉再起(送訊號前先確認 pid 真的是 postgres)
//   - 資料目錄保護:函式庫在 pgdata/PG_VERSION 讀不到或版本不符時會「先整個刪掉資料目錄再重新初始化」,
//     所以啟動前先自己檢查(checkPGDataDir),有資料卻對不上就拒絕啟動,不讓函式庫動它
//   - 下載完整性:函式庫在 sha256 檔抓不到時會略過驗證,所以自己下載並用寫死的 sha256 比對(embeddedpg_pin.go)
//   - PostgreSQL 不能以 root(Unix)或系統管理員權限(Windows)執行,這時候回傳說得清楚的錯誤,請改用一般使用者執行,
//     或用 OMIXOR_DATABASE_URL 連自己的 PostgreSQL
//
// 使用者已經設了 OMIXOR_DATABASE_URL 時不會走這裡(見 OpenLocal)。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"

	"omixor-core/config"
)

const (
	embeddedPGUser = "omixor"
	embeddedPGDB   = "omixor"
)

// EmbeddedPGOptions 內建 PostgreSQL 的設定
type EmbeddedPGOptions struct {
	// Dir 資料夾(裡面放 pgdata、pgruntime、pgpassword)
	Dir string
	// Logger 啟動過程的訊息(PostgreSQL 與 initdb 的輸出)寫到這裡;nil = 只留最後一段,失敗時放進錯誤訊息
	Logger io.Writer
	// StartTimeout 整個啟動(含第一次下載與解壓)最多等多久;0 = 10 分鐘
	StartTimeout time.Duration
	// Context 取消時中止啟動(收到結束訊號):等背景的啟動結束並把已經起來的 PostgreSQL 關掉;nil = 不取消
	Context context.Context
	// CacheDir 二進位檔的快取資料夾;空的 = <使用者家目錄>/.embedded-postgres-go(函式庫的預設)
	CacheDir string
	// Fast 不 fsync(只給測試用,資料不保證撐得過當機)
	Fast bool
}

// EmbeddedPG 一個在跑的內建 PostgreSQL
type EmbeddedPG struct {
	db                  *embeddedpostgres.EmbeddedPostgres
	URL                 string // 連線網址(只在這個程式的這次啟動有效)
	runtimeDir, dataDir string
	mu                  sync.Mutex
}

// tailBuffer 只留最後一段輸出的緩衝(錯誤訊息用)
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 16 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailMax:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// embeddedPGPassword 讀(沒有就產生並存起來)資料夾的 pgpassword
func embeddedPGPassword(dir string) (string, error) {
	p := filepath.Join(dir, "pgpassword")
	if b, err := os.ReadFile(p); err == nil {
		if pw := strings.TrimSpace(string(b)); pw != "" {
			return pw, nil
		}
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	pw := hex.EncodeToString(raw)
	if err := os.WriteFile(p, []byte(pw+"\n"), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

// pickPort 挑一個目前沒被佔用的本機埠(127.0.0.1)
func pickPort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

// pgCtl 上次解壓出來的 pg_ctl 路徑(沒有就是空的)
func pgCtl(runtimeDir string) string {
	for _, n := range []string{"pg_ctl.exe", "pg_ctl"} {
		p := filepath.Join(runtimeDir, "bin", n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// pinnedPGVersion 釘住的 PostgreSQL 版本(升級要連資料搬遷一起考慮,不要只改這裡)
const pinnedPGVersion = embeddedpostgres.V17

// checkPGDataDir 啟動前檢查資料目錄。函式庫的 Start 在 PG_VERSION 讀不到或和設定的版本對不上時,
// 會先 os.RemoveAll(資料目錄) 再重新 initdb,資料就全沒了(防毒鎖檔、權限、升版本、資料夾被還原成舊內容都可能遇到)。
// 所以:
//   - 不存在或是空的:可以讓函式庫初始化
//   - 有 PG_VERSION 而且主版本相同:沒問題
//   - 沒有 global/pg_control 而且 base 裡最多只有 template1:上一次第一次初始化(initdb)被中斷,還沒有任何資料;
//     把它改名成 pgdata.incomplete-<時間> 留著(不刪),讓函式庫重新初始化
//   - 其他(PG_VERSION 不存在、讀不到、主版本不符):拒絕啟動,回傳說明怎麼處理的錯誤
func checkPGDataDir(dataDir string, version embeddedpostgres.PostgresVersion) error {
	ents, err := os.ReadDir(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("讀不到內建 PostgreSQL 的資料目錄 %s:%w\n為了不讓資料被清掉,拒絕啟動。請確認權限或有沒有別的程式(防毒軟體)鎖住它", dataDir, err)
	}
	if len(ents) == 0 {
		return nil
	}
	want := strings.SplitN(string(version), ".", 2)[0]
	b, verr := os.ReadFile(filepath.Join(dataDir, "PG_VERSION"))
	if verr == nil {
		if got := strings.TrimSpace(string(b)); got != want {
			return fmt.Errorf("內建 PostgreSQL 的資料目錄 %s 是 PostgreSQL %s 版的資料,這個程式內建的是 %s 版;"+
				"為了不讓資料被清掉,拒絕啟動。請先備份整個 %s 資料夾,再確認 omixor-local 的版本是否正確"+
				"(升級 PostgreSQL 大版本需要先用舊版匯出資料);確定資料不要了才把資料夾移走",
				dataDir, got, want, dataDir)
		}
		return nil
	}
	_, cerr := os.Stat(filepath.Join(dataDir, "global", "pg_control"))
	base, _ := os.ReadDir(filepath.Join(dataDir, "base"))
	if errors.Is(cerr, os.ErrNotExist) && len(base) <= 1 {
		dst := fmt.Sprintf("%s.incomplete-%s", dataDir, time.Now().Format("20060102-150405"))
		if err := os.Rename(dataDir, dst); err != nil {
			return fmt.Errorf("資料目錄 %s 沒有初始化完成,想改名保留成 %s 失敗:%w", dataDir, dst, err)
		}
		log.Printf("資料目錄 %s 上次沒有初始化完成(還沒有資料),已改名成 %s 保留,重新初始化", dataDir, dst)
		return nil
	}
	return fmt.Errorf("內建 PostgreSQL 的資料目錄 %s 裡沒有 PG_VERSION(或讀不到:%v),但有其他檔案;"+
		"為了不讓資料被清掉,拒絕啟動。請先備份整個 %s 資料夾,確認沒有別的程式(防毒軟體)鎖住或動過它,"+
		"沒有資料需要保留時才把資料夾移走再重開", dataDir, verr, dataDir)
}

// warnLegacyStore 升級前的檔案版 store(<資料夾>/store.json)已經不再使用,而且還沒有 pgdata(= 還沒有用過新版):
// 資料不會自動匯入,印出明確警告。不會刪 store.json。回傳有沒有警告
func warnLegacyStore(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "store.json")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "pgdata")); err == nil {
		return false
	}
	log.Printf("警告:%s 是舊版(檔案版)的資料,新版改存內建的 PostgreSQL,舊資料「沒有」自動匯入,"+
		"會以新的空資料庫啟動。macOS App 的 Issue、角色、文件櫃等會由 App 從組織資料夾的 JSON 重新匯入;"+
		"其他資料如果還需要,請保留 store.json(程式不會刪它)並另外處理,確定不需要才自行刪除", filepath.Join(dir, "store.json"))
	return true
}

// readPostmasterPID 讀資料目錄 postmaster.pid 的第一行(行程 id)
func readPostmasterPID(dataDir string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, "postmaster.pid"))
	if err != nil {
		return 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// pidIsPostgres 這個 pid 的行程是不是 postgres。known=false 表示判斷不了(這時候不要送訊號)
func pidIsPostgres(pid int) (isPG, known bool) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return false, false
		}
		// 沒有這個行程時輸出 INFO: No tasks...;有就是 "映像名稱","pid",...
		return strings.Contains(strings.ToLower(string(out)), "postgres"), true
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		return strings.Contains(strings.ToLower(strings.TrimSpace(string(b))), "postgres"), true
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		// ps 找不到該行程會回非 0(行程不存在);ps 本身不能用就判斷不了
		if _, ok := err.(*exec.ExitError); ok {
			return false, true
		}
		return false, false
	}
	return strings.Contains(strings.ToLower(filepath.Base(strings.TrimSpace(string(out)))), "postgres"), true
}

// stopStale 上次沒關乾淨時關掉還活著的 postgres(資料目錄有 postmaster.pid 才需要)。回傳有沒有嘗試。
// 送訊號前先確認 pid 真的是 postgres:pid 檔過期、pid 被別的行程重用時 pg_ctl 會直接對那個行程送訊號,所以確認不了就不送
func stopStale(runtimeDir, dataDir string) bool {
	if _, err := os.Stat(filepath.Join(dataDir, "postmaster.pid")); err != nil {
		return false
	}
	exe := pgCtl(runtimeDir)
	if exe == "" {
		return false
	}
	if pid, ok := readPostmasterPID(dataDir); ok {
		isPG, known := pidIsPostgres(pid)
		if !known {
			log.Printf("資料目錄有 postmaster.pid(pid %d),但判斷不了那個行程是不是 PostgreSQL,不送關閉訊號,當成過期的 pid 檔交給 PostgreSQL 處理", pid)
			return false
		}
		if !isPG {
			log.Printf("資料目錄的 postmaster.pid(pid %d)指的行程不是 PostgreSQL(過期的 pid 檔或 pid 被重用),不送關閉訊號", pid)
			return false
		}
	}
	log.Printf("資料目錄有上次沒關乾淨的 postmaster.pid,先嘗試關掉還在跑的 PostgreSQL")
	// 沒有在跑(pid 檔過期)時 pg_ctl 會回錯誤,不用理會:之後的啟動由 PostgreSQL 自己處理過期的 pid 檔
	_ = exec.Command(exe, "stop", "-m", "fast", "-w", "-t", "30", "-D", dataDir).Run()
	waitRuntimeReleased(runtimeDir, 20*time.Second)
	return true
}

// stopPostgresFast 用 pg_ctl stop -m fast 關掉資料目錄的 postgres(沒在跑也沒關係)。回傳關閉有沒有成功
func stopPostgresFast(runtimeDir, dataDir string, wait time.Duration) bool {
	exe := pgCtl(runtimeDir)
	if exe == "" {
		return false
	}
	ok := exec.Command(exe, "stop", "-m", "fast", "-w", "-t", strconv.Itoa(int(wait.Seconds())), "-D", dataDir).Run() == nil
	waitRuntimeReleased(runtimeDir, 20*time.Second)
	return ok
}

// waitRuntimeReleased postgres 關掉之後,Windows 要過一下才放開 pgruntime 裡的 postgres.exe;
// 這時候馬上刪或重建 runtime 資料夾會失敗(unlinkat … The process cannot access the file)。
// 輪詢到能以寫入模式開啟(沒有行程還在用)或逾時為止;其他系統一開始就能開,馬上返回
func waitRuntimeReleased(runtimeDir string, max time.Duration) {
	var exe string
	for _, n := range []string{"postgres.exe", "postgres"} {
		p := filepath.Join(runtimeDir, "bin", n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			exe = p
			break
		}
	}
	if exe == "" {
		return
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		f, err := os.OpenFile(exe, os.O_RDWR, 0)
		if err == nil {
			f.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("等了 %s,pgruntime 裡的 postgres 還沒釋放;之後重建 runtime 資料夾可能會失敗", max)
}

// runAbandonable 在背景跑 start,等它做完或 ctx 結束。ctx 結束時:再多等 grace 讓它做完;
// 做完而且成功(postgres 已經起來了)就呼叫 cleanup 關掉;grace 過了還沒做完,先呼叫 cleanup,
// 並留一個背景工作在它之後成功時再呼叫一次 cleanup,避免留下沒人管的孤兒 postgres。ctx 結束時回傳 ctx.Err()
func runAbandonable(ctx context.Context, start func() error, cleanup func(), grace time.Duration) error {
	res := make(chan error, 1)
	go func() { res <- start() }()
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
	}
	select {
	case err := <-res:
		if err == nil {
			cleanup()
		}
	case <-time.After(grace):
		go func() {
			if err := <-res; err == nil {
				cleanup()
			}
		}()
		cleanup()
	}
	return ctx.Err()
}

// ensureDatabase 連 postgres 庫,omixor 資料庫不存在就建立(函式庫只有在「第一次初始化」時才會建,
// 初始化完成後、建資料庫前被中斷,之後函式庫會當成已經有了而跳過)
func ensureDatabase(ctx context.Context, port uint32, pw string) error {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(embeddedPGUser, pw), Host: fmt.Sprintf("127.0.0.1:%d", port),
		Path: "/postgres", RawQuery: "sslmode=disable"}
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	var one int
	err = conn.QueryRow(ctx, "select 1 from pg_database where datname = $1", embeddedPGDB).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	log.Printf("資料庫 %s 不存在(上次初始化被中斷?),建立", embeddedPGDB)
	_, err = conn.Exec(ctx, fmt.Sprintf(`create database "%s"`, embeddedPGDB))
	return err
}

// pgctlSetByUs 本行程設進 PGCTLTIMEOUT 的值(空 = 還沒設過);用來分辨「使用者自己設的」與「上一次啟動我們設的」
var (
	pgctlMu      sync.Mutex
	pgctlSetByUs string
)

// applyPGCtlTimeout 使用者沒設 PGCTLTIMEOUT(或值是上一次我們自己設的)才設成「啟動逾時 - 60 秒」(至少 60)
func applyPGCtlTimeout(cfg *config.Config, timeout time.Duration) {
	pgctlMu.Lock()
	defer pgctlMu.Unlock()
	if cfg.IsSet(config.PGCtlTimeout) && cfg.Str(config.PGCtlTimeout) != pgctlSetByUs {
		return
	}
	secs := int(timeout.Seconds()) - 60
	if secs < 60 {
		secs = 60
	}
	pgctlSetByUs = strconv.Itoa(secs)
	_ = os.Setenv("PGCTLTIMEOUT", pgctlSetByUs)
}

// repairDatabase 自己用 pg_ctl 起資料目錄的 postgres,確認 omixor 資料庫存在(沒有就建),再關掉
func repairDatabase(ctx context.Context, runtimeDir, dataDir, pw string) error {
	exe := pgCtl(runtimeDir)
	if exe == "" {
		return errors.New("找不到 pg_ctl")
	}
	port, err := pickPort()
	if err != nil {
		return err
	}
	// 輸出導到檔案:postgres 會繼承 pg_ctl 的標準輸出,用管道接(CombinedOutput)會一直等到 postgres 結束才返回
	lf, err := os.CreateTemp("", "omixor-pgrepair-*.log")
	if err != nil {
		return err
	}
	defer os.Remove(lf.Name())
	defer lf.Close()
	cmd := exec.CommandContext(ctx, exe, "start", "-w", "-D", dataDir, "-o", fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1", port))
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Run(); err != nil {
		out, _ := os.ReadFile(lf.Name())
		return fmt.Errorf("pg_ctl start 失敗:%v\n%s", err, strings.TrimSpace(string(out)))
	}
	defer stopPostgresFast(runtimeDir, dataDir, 30*time.Second)
	return ensureDatabase(ctx, port, pw)
}

// waitRecovered pg_ctl start -w 逾時但 postgres 還在啟動(崩潰恢復)時:不要關掉它(關了下次又得從頭恢復,變成重啟迴圈),
// 等它恢復完成可以連線,再關掉重來(這次不用再恢復,很快)
func waitRecovered(ctx context.Context, runtimeDir, dataDir, pw string, port uint32) error {
	log.Print("PostgreSQL 還在做崩潰恢復(資料量大時會比較久),等它完成,請不要關閉程式")
	for {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ensureDatabase(cctx, port, pw)
		cancel()
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	stopPostgresFast(runtimeDir, dataDir, 120*time.Second)
	return nil
}

// portBusy 啟動失敗的訊息是不是「埠被別人佔走了」(挑完埠到真的 listen 之間被搶;各平台的字串不同)
func portBusy(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{"already listening", "address already in use", "only one usage of each socket address"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// libHealthTimeout 函式庫啟動後等資料庫可以連線最多多久(測試會調短)
var libHealthTimeout = 45 * time.Second

// StartEmbeddedPG 起內建的 PostgreSQL 並等它可以連線。要用 Stop 關掉
func StartEmbeddedPG(o EmbeddedPGOptions) (*EmbeddedPG, error) {
	if o.Dir == "" {
		return nil, errors.New("內建 PostgreSQL 要給資料夾")
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		return nil, errors.New("內建 PostgreSQL 不能以 root 執行(PostgreSQL 的限制):請改用一般使用者執行 omixor-local," +
			"或設 OMIXOR_DATABASE_URL 連自己的 PostgreSQL")
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, err
	}
	dataDir, runtimeDir := filepath.Join(o.Dir, "pgdata"), filepath.Join(o.Dir, "pgruntime")
	// 先檢查資料目錄:有資料卻對不上就拒絕,絕不讓函式庫去清它
	if err := checkPGDataDir(dataDir, pinnedPGVersion); err != nil {
		return nil, err
	}
	pw, err := embeddedPGPassword(o.Dir)
	if err != nil {
		return nil, fmt.Errorf("建立 %s 失敗:%w", filepath.Join(o.Dir, "pgpassword"), err)
	}
	timeout := o.StartTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	parent := o.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// pg_ctl start -w 預設只等 60 秒,大資料庫崩潰恢復可能更久;PGCTLTIMEOUT 是 pg_ctl 認的預設等待秒數(使用者自己設了就用他的)
	// 「使用者自己設的」要排除我們上一次(同一行程)設進去的值,不然第二次啟動會把自己設的當成使用者的
	applyPGCtlTimeout(config.Current(), timeout)

	params := map[string]string{"listen_addresses": "127.0.0.1"}
	if o.Fast {
		params["fsync"], params["synchronous_commit"], params["full_page_writes"] = "off", "off", "off"
	}
	var tail tailBuffer
	var lw io.Writer = &tail
	if o.Logger != nil {
		lw = io.MultiWriter(&tail, o.Logger)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); err != nil {
		log.Print("啟動內建 PostgreSQL(第一次:要下載 PostgreSQL 二進位檔並初始化資料目錄,需要網路,可能要幾十秒到幾分鐘)")
	} else {
		log.Print("啟動內建 PostgreSQL")
	}
	cacheDir, err := ensureVerifiedBinaries(ctx, o.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("內建 PostgreSQL 啟動失敗(下載 download PostgreSQL 二進位檔):%w", err)
	}
	stopStale(runtimeDir, dataDir)

	abort := func(err error) error {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("內建 PostgreSQL 在 %s 內沒有啟動好(第一次啟動要下載二進位檔,網路是不是不通?):\n%s", timeout, tail.String())
		}
		return fmt.Errorf("啟動內建 PostgreSQL 被中止:%w", err)
	}
	// ctx 結束後多等背景的啟動多久:被取消(收到結束訊號)不能等太久;逾時多等一些讓快做完的啟動收尾再關掉
	grace := 30 * time.Second
	if parent.Err() != nil {
		grace = 4 * time.Second
	}
	var lastErr error
	triedStale, triedRepair := false, false
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			return nil, abort(ctx.Err())
		}
		port, err := pickPort()
		if err != nil {
			return nil, fmt.Errorf("找不到可用的本機埠:%w", err)
		}
		cfg := embeddedpostgres.DefaultConfig().
			Version(pinnedPGVersion).
			Port(port).
			Username(embeddedPGUser).
			Password(pw).
			Database(embeddedPGDB).
			RuntimePath(runtimeDir).
			DataPath(dataDir).
			StartTimeout(libHealthTimeout).
			Logger(lw).
			StartParameters(params)
		if cacheDir != "" {
			cfg = cfg.CachePath(cacheDir)
		}
		db := embeddedpostgres.NewDatabase(cfg)
		err = runAbandonable(ctx, db.Start, func() { stopPostgresFast(runtimeDir, dataDir, 20*time.Second) }, grace)
		if ctx.Err() != nil && err == ctx.Err() {
			return nil, abort(ctx.Err())
		}
		if err == nil {
			u := url.URL{Scheme: "postgres", User: url.UserPassword(embeddedPGUser, pw), Host: fmt.Sprintf("127.0.0.1:%d", port),
				Path: "/" + embeddedPGDB, RawQuery: "sslmode=disable"}
			return &EmbeddedPG{db: db, URL: u.String(), runtimeDir: runtimeDir, dataDir: dataDir}, nil
		}
		lastErr = err
		msg := err.Error()
		if portBusy(msg) { // 剛挑的埠被別人搶走了:換一個
			continue
		}
		// pg_ctl 等逾時但 postgres 還在恢復:不要關掉它,等它恢復完成再重來
		if strings.Contains(msg, "did not start in time") || strings.Contains(strings.ToLower(msg), "still starting up") {
			if werr := waitRecovered(ctx, runtimeDir, dataDir, pw, port); werr != nil {
				return nil, abort(werr)
			}
			continue
		}
		// 資料目錄是有效的、postgres 也起得來,卻連不到 omixor 資料庫(上次初始化在建資料庫之前被中斷):補建後重來
		if !triedRepair && strings.Contains(msg, "timed out waiting for database to become available") {
			triedRepair = true
			if rerr := repairDatabase(ctx, runtimeDir, dataDir, pw); rerr != nil {
				log.Printf("補建資料庫失敗:%v", rerr)
			} else {
				continue
			}
		}
		// 資料目錄有 postmaster.pid 而且啟動失敗:可能是上次的 postgres 還活著(這次的啟動已經把程式解壓好了,可以用它的 pg_ctl)
		if !triedStale && stopStale(runtimeDir, dataDir) {
			triedStale = true
			continue
		}
		break
	}
	msg := lastErr.Error()
	if t := tail.String(); t != "" && !strings.Contains(msg, t) {
		msg += "\n" + t
	}
	hint := ""
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "administrative permissions") || strings.Contains(low, "root"):
		hint = "(PostgreSQL 不能用系統管理員或 root 身分執行,請改用一般使用者;或設 OMIXOR_DATABASE_URL 連自己的 PostgreSQL)"
	case strings.Contains(low, "password authentication failed"):
		hint = fmt.Sprintf("(資料目錄的密碼和 %s 對不起來;如果 pgpassword 弄丟了,資料目錄 %s 無法再使用)", filepath.Join(o.Dir, "pgpassword"), dataDir)
	case strings.Contains(low, "download") || strings.Contains(low, "dial tcp") || strings.Contains(low, "no such host") || strings.Contains(low, "repo1.maven.org"):
		hint = "(第一次啟動要從網路下載 PostgreSQL 二進位檔;網路不通時可以改設 OMIXOR_DATABASE_URL 連自己的 PostgreSQL)"
	}
	return nil, fmt.Errorf("內建 PostgreSQL 啟動失敗%s:%s", hint, msg)
}

// Stop 關掉內建的 PostgreSQL(pg_ctl stop -m fast,等它把資料寫好)。重複呼叫沒有作用
func (e *EmbeddedPG) Stop() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return nil
	}
	db := e.db
	e.db = nil
	// 用 fast 模式:函式庫的 Stop 是預設的 smart 模式,要等所有連線都斷了,有連線卡著就會等到逾時
	if stopPostgresFast(e.runtimeDir, e.dataDir, 30*time.Second) {
		return nil
	}
	if err := db.Stop(); err != nil {
		return fmt.Errorf("關閉內建 PostgreSQL 失敗:%w", err)
	}
	return nil
}
