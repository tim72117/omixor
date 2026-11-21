package server

// 內建 PostgreSQL 二進位檔的完整性:embedded-postgres 函式庫從 Maven Central 下載 jar 時,
// 如果 .sha256 檔抓不到或不是 200 就「靜默略過」驗證,而且已經在快取裡的檔案完全不檢查。
// 所以釘住的版本(embeddedpg.go 的 pinnedPGVersion)在這裡自己下載、自己比對寫死的 sha256:
//   - jar 的 sha256 = Maven Central 上 .jar.sha256 的內容(下載時比對)
//   - txz 的 sha256 = jar 裡那個 .txz 的雜湊(每次啟動比對快取檔,不符就刪掉重抓)
//
// 升級 PostgreSQL 版本時,這兩張表要一起更新(用 curl 抓 .jar.sha256,解開 jar 對 .txz 做 sha256sum)。
// 不在表裡的平台(例如 windows/arm64,函式庫與上游都沒有這個平台的二進位檔)沿用函式庫自己的下載,不做這層驗證。

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// pgBinaryRepo 二進位檔來源(和函式庫預設的相同)
var pgBinaryRepo = "https://repo1.maven.org/maven2"

type pgPin struct{ jar, txz string }

// pgPins 17.5.0 各平台;key = 函式庫的 <作業系統>-<架構>(linux arm64 是 arm64v8,alpine 加 -alpine,macOS arm64 是 arm64v8)
var pgPins = map[string]pgPin{
	"windows-amd64":        {"06ced3b3267c569ffb3f85544aac0075926dd81bf78bd5247abb1aa9fe994d6b", "a7f4baf26bffb8dbb0d07758c1c44ee8a518bedbe01c6017b68c60595c2233ab"},
	"darwin-arm64v8":       {"e9d3398e10c2ec926395498b03e75ad1a24eeaed82895e756a7e173b202cf6de", "746918d0d88eb766e51b43d93ac0d4dbcd6730ff0ac61399a4fcfd626d6ea287"},
	"darwin-amd64":         {"fb70f783928e77c3d6ef5ca8ed644f1ac502f740fcfc032a375c8347820f2bdd", "9418e2e93bf4a767dcd56b417c0b823ab237f935ad2460df1ce43ed65ea017c3"},
	"linux-amd64":          {"646f3269075d1a23904127374e3cdbcef96beb32979fedee25d835877400fcbe", "8f438e206259d3ec5be6fb8ee6180f5d46234156659ed1238f6ab4806cfccf0f"},
	"linux-arm64v8":        {"e02f27ff50ff2d90b6541d54f3f108505aacd84ea53c2890e976a684bfea2752", "b931564ba20d4009d70c37ceb27109213f696cfd507c4ecb769c6d6762454a1c"},
	"linux-amd64-alpine":   {"7122386c7f7a089568f05720ada63448093d4065abbc0a89e6dc660851a53e3c", "9bbbdc4a892b02911c3825ee6e29ffc2cc73df40acfcebf41a1ca3c5f804edb3"},
	"linux-arm64v8-alpine": {"f9cf1d22dd98bfdb63738846c330f2b9f18adc39c927b3a082d0ccbe51a0efa1", "022d6506180105ee9b0afd6358dcf0247daf9dd679b440843a153ac653c4e169"},
}

// pgPlatformKey 目前平台在函式庫裡的名字(和 embedded-postgres 的 defaultVersionStrategy 一致)
func pgPlatformKey() string {
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "linux":
		if arch == "arm64" {
			arch = "arm64v8"
		}
		if _, err := os.Stat("/etc/alpine-release"); err == nil {
			arch += "-alpine"
		}
	case "darwin":
		if arch == "arm64" {
			arch = "arm64v8"
		}
	}
	return runtime.GOOS + "-" + arch
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensureVerifiedBinaries 確保快取裡的二進位檔(txz)存在而且雜湊對得上;沒有或不對就下載(jar 雜湊也要對)。
// 回傳快取資料夾(給函式庫 CachePath 用)。目前平台不在 pgPins 裡就什麼都不做(函式庫照原本的方式處理)
func ensureVerifiedBinaries(ctx context.Context, cacheDir string) (string, error) {
	if cacheDir == "" {
		cacheDir = ".embedded-postgres-go"
		if home, err := os.UserHomeDir(); err == nil {
			cacheDir = filepath.Join(home, ".embedded-postgres-go")
		}
	}
	key := pgPlatformKey()
	pin, ok := pgPins[key]
	if !ok {
		return cacheDir, nil
	}
	ver := string(pinnedPGVersion)
	cache := filepath.Join(cacheDir, fmt.Sprintf("embedded-postgres-binaries-%s-%s.txz", key, ver))
	if _, err := os.Stat(cache); err == nil {
		got, herr := sha256File(cache)
		if herr == nil && got == pin.txz {
			return cacheDir, nil
		}
		log.Printf("PostgreSQL 二進位檔快取 %s 的雜湊對不上(檔案損壞或被改過),刪掉重新下載", cache)
		if err := os.Remove(cache); err != nil {
			return "", fmt.Errorf("刪不掉損壞的快取 %s:%w", cache, err)
		}
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("embedded-postgres-binaries-%s-%s", key, ver)
	u := fmt.Sprintf("%s/io/zonky/test/postgres/embedded-postgres-binaries-%s/%s/%s.jar", pgBinaryRepo, key, ver, name)
	log.Printf("下載 PostgreSQL 二進位檔:%s", u)
	jar, err := httpGetAll(ctx, u)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(jar); hex.EncodeToString(sum[:]) != pin.jar {
		return "", errors.New("下載的 PostgreSQL 二進位檔雜湊和程式內建的對不上,已拒絕使用(下載被竄改或損壞?)")
	}
	zr, err := zip.NewReader(bytes.NewReader(jar), int64(len(jar)))
	if err != nil {
		return "", fmt.Errorf("解開下載的檔案失敗:%w", err)
	}
	var txz []byte
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && strings.HasSuffix(f.Name, ".txz") {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			txz, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			break
		}
	}
	if txz == nil {
		return "", errors.New("下載的檔案裡找不到 PostgreSQL 二進位檔(.txz)")
	}
	if sum := sha256.Sum256(txz); hex.EncodeToString(sum[:]) != pin.txz {
		return "", errors.New("解開的 PostgreSQL 二進位檔雜湊和程式內建的對不上,已拒絕使用")
	}
	// 先寫暫存檔再改名,避免半個檔案被當成快取(多個行程同時下載也不會互相破壞)
	tmp, err := os.CreateTemp(cacheDir, "temp_")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(txz)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), cache); err != nil {
		_ = os.Remove(tmp.Name())
		if _, serr := os.Stat(cache); serr != nil { // 別的行程剛好放好了就算成功
			return "", err
		}
	}
	return cacheDir, nil
}

func httpGetAll(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("連不到 %s:%w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下載 %s 失敗:HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
