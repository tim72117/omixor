package server

// omixor-local 的進入點:解析旗標與環境變數、鎖資料夾、啟動 HTTP、等結束訊號(設定見 local.go 與 standalone.go 的檔頭)

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"omixor-core/version"
)

// localOptions 命令列旗標與環境變數合起來的設定
type localOptions struct {
	Standalone bool
	Rotate     bool
	Version    bool
	Addr       string
	Cfg        LocalConfig
	ExitOnEOF  bool
}

const localUsage = `omixor-local:omixor 本機守護服務(只聽本機 127.0.0.1,資料存在資料夾的 JSON)

用法:
  omixor-local [旗標]

兩種模式:
  App 模式    macOS App 在背景啟動,用環境變數 OMIXOR_LOCAL_TOKEN 給 token(使用者不用手動跑)
  獨立模式    沒給 OMIXOR_LOCAL_TOKEN 或加 -standalone 時。第一次啟動產生 token,存在
              <資料夾>/credentials.json,之後沿用。Ctrl+C 或關閉視窗結束

旗標:
  -standalone      強制用獨立模式(忽略 OMIXOR_LOCAL_TOKEN)
  -data 資料夾      資料夾(預設 OMIXOR_DATA_DIR;獨立模式再預設 <使用者設定資料夾>/omixor/core,
                   Windows 是 %%AppData%%\omixor\core)
  -addr 位址        監聽位址(預設 OMIXOR_ADDR;獨立模式再預設 %s,App 模式 127.0.0.1:0);只接受本機位址
  -rotate-token    (獨立模式)換一把新的 token,舊的立刻作廢,然後照常啟動
  -version         印出版本後結束
  -h               這份說明

環境變數:OMIXOR_DATA_DIR、OMIXOR_ADDR、OMIXOR_LOCAL_TOKEN、OMIXOR_LOCAL_USER(預設 local)、
          OMIXOR_LOCAL_NAME、OMIXOR_LOCAL_ORG(預設 default)、OMIXOR_EXIT_ON_STDIN_EOF=1(只在 App 模式有作用)

用 omixor CLI 連這台(獨立模式):
  OMIXOR_SERVER=http://%s OMIXOR_CREDENTIALS=<資料夾>/credentials.json omixor orgs
`

// parseLocalArgs 旗標 > 環境變數 > 預設值
func parseLocalArgs(args []string, getenv func(string) string, errOut io.Writer) (localOptions, error) {
	fs := flag.NewFlagSet("omixor-local", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() { fmt.Fprintf(errOut, localUsage, StandaloneAddr, StandaloneAddr) }
	var o localOptions
	var data, addr string
	fs.BoolVar(&o.Standalone, "standalone", false, "強制用獨立模式")
	fs.StringVar(&data, "data", "", "資料夾")
	fs.StringVar(&addr, "addr", "", "監聽位址")
	fs.BoolVar(&o.Rotate, "rotate-token", false, "換一把新的 token")
	fs.BoolVar(&o.Version, "version", false, "印出版本")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return o, fmt.Errorf("不認得的參數:%s", strings.Join(fs.Args(), " "))
	}
	if o.Version {
		return o, nil
	}
	o.Cfg = LocalConfig{DataDir: getenv("OMIXOR_DATA_DIR"), Token: getenv("OMIXOR_LOCAL_TOKEN"),
		User: getenv("OMIXOR_LOCAL_USER"), Name: getenv("OMIXOR_LOCAL_NAME"), Org: getenv("OMIXOR_LOCAL_ORG")}
	if o.Cfg.Token == "" {
		o.Standalone = true
	}
	if data != "" {
		o.Cfg.DataDir = data
	}
	o.Addr = getenv("OMIXOR_ADDR")
	if addr != "" {
		o.Addr = addr
	}
	if o.Standalone {
		o.Cfg.Token = "" // 獨立模式的 token 一律來自 credentials.json
		if o.Cfg.DataDir == "" {
			d, err := DefaultStandaloneDir()
			if err != nil {
				return o, err
			}
			o.Cfg.DataDir = d
		}
		if o.Addr == "" {
			o.Addr = StandaloneAddr
		}
	} else {
		if o.Rotate {
			return o, errors.New("-rotate-token 只在獨立模式用(App 模式每次啟動本來就換新的 token)")
		}
		if o.Addr == "" {
			o.Addr = "127.0.0.1:0"
		}
		o.ExitOnEOF = getenv("OMIXOR_EXIT_ON_STDIN_EOF") == "1"
	}
	if err := loopbackAddr(o.Addr); err != nil {
		return o, err
	}
	if o.Cfg.DataDir == "" {
		return o, errors.New("要設 OMIXOR_DATA_DIR")
	}
	return o, nil
}

// importWait 結束時有匯入在跑,最多等多久
var importWait = 30 * time.Second

// RunLocal omixor-local 的進入點
func RunLocal() {
	log.SetPrefix("omixor-local ")
	tok := os.Getenv("OMIXOR_LOCAL_TOKEN")
	_ = os.Unsetenv("OMIXOR_LOCAL_TOKEN") // 用過就從環境拿掉(子程序不會繼承)
	getenv := func(k string) string {
		if k == "OMIXOR_LOCAL_TOKEN" {
			return tok
		}
		return os.Getenv(k)
	}
	err := runLocal(os.Args[1:], getenv, os.Stdout, os.Stderr, nil)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Print(err)
		if runtime.GOOS == "windows" && tok == "" {
			// Windows 雙擊啟動時視窗會馬上關掉,停一下讓人看得到錯誤
			fmt.Fprintln(os.Stderr, "按 Enter 關閉…")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		os.Exit(1)
	}
}

// runLocal 啟動並一直跑到收到結束訊號(stop 關掉、Ctrl+C、SIGTERM、App 模式的標準輸入關閉);
// 正常結束回傳 nil。stop 給測試用,可以是 nil
func runLocal(args []string, getenv func(string) string, stdout, stderr io.Writer, stop <-chan struct{}) error {
	o, err := parseLocalArgs(args, getenv, stderr)
	if err != nil {
		return err
	}
	if o.Version {
		fmt.Fprintf(stdout, "omixor-local %s\n", version.Info())
		return nil
	}
	if o.Standalone && getenv("OMIXOR_LOCAL_TOKEN") != "" {
		log.Print("有 -standalone:不用 OMIXOR_LOCAL_TOKEN,改用資料夾裡 credentials.json 的 token")
	}
	lock, err := lockDir(o.Cfg.DataDir)
	if err != nil {
		if o.Rotate {
			return fmt.Errorf("%v;要換 token 請先結束正在跑的那一個", err)
		}
		return err
	}
	defer lock.Close()
	var l *Local
	if o.Standalone {
		l, err = OpenStandalone(o.Cfg, o.Rotate)
	} else {
		l, err = OpenLocal(o.Cfg)
	}
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		if o.Standalone {
			return fmt.Errorf("%s 不能用(是不是已經有一個在跑?可以用 -addr 或 OMIXOR_ADDR 換位址):%w", o.Addr, err)
		}
		return err
	}
	url := "http://" + ln.Addr().String()
	if o.Standalone {
		if err := l.SaveStandaloneCred(url); err != nil {
			ln.Close()
			return fmt.Errorf("寫 %s 失敗:%w", StandaloneCredPath(o.Cfg.DataDir), err)
		}
	}
	// 領取任務時附上角色端點的網址(App 的自動執行用)
	_ = os.Setenv("OMIXOR_PUBLIC_URL", url)
	stopRoutines := l.Svc.StartRoutines(time.Minute, nil)
	hs := &http.Server{Handler: l.Handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	var once sync.Once
	shutdown := func(why string) {
		once.Do(func() {
			log.Printf("結束(%s)", why)
			stopRoutines()
			// 資料每次變更就寫進資料夾(core/store/file.go),這裡只要等進行中的請求做完;
			// Windows 關閉主控台視窗時大約只有 5 秒,所以平常等 3 秒就好。
			// 有匯入在跑(App 第一次搬資料,可能要好幾秒)時最多等 importWait:每一類匯入是全有或全無,
			// 真的被砍斷也只是那一類沒寫入、之後重送,但盡量讓它做完
			wait := 3 * time.Second
			if l.Importing() {
				wait = importWait
				log.Printf("有匯入在進行,最多等 %s 讓它做完", wait)
			}
			ctx, cancel := context.WithTimeout(context.Background(), wait)
			defer cancel()
			_ = hs.Shutdown(ctx)
			close(done)
		})
	}
	// os.Interrupt:Ctrl+C(Windows 也是);SIGTERM:kill,Windows 關閉主控台視窗、登出、關機時 Go 也送這個
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		select {
		case s := <-sig:
			shutdown(s.String())
		case <-done:
		}
	}()
	if stop != nil {
		go func() {
			select {
			case <-stop:
				shutdown("要求結束")
			case <-done:
			}
		}()
	}
	if o.ExitOnEOF {
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); shutdown("App 關掉了標準輸入") }()
	}
	go func() {
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP:%v", err)
			shutdown("HTTP 錯誤")
		}
	}()
	if o.Standalone {
		log.Printf("獨立模式,在 %s,資料夾 %s", url, o.Cfg.DataDir)
		log.Printf("token 在 %s(只有這個使用者能讀);omixor CLI:OMIXOR_SERVER=%s OMIXOR_CREDENTIALS=%s omixor orgs",
			StandaloneCredPath(o.Cfg.DataDir), url, StandaloneCredPath(o.Cfg.DataDir))
		if o.Rotate {
			log.Print("已換新的 token,舊的已作廢")
		}
		log.Print("按 Ctrl+C 結束")
	} else {
		log.Printf("在 %s,資料夾 %s", url, o.Cfg.DataDir)
	}
	fmt.Fprintf(stdout, "OMIXOR_LOCAL_READY %s\n", url)
	<-done
	return nil
}
