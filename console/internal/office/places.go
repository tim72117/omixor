package office

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// office3d 資料夾:OMIXOR_OFFICE_DIR,否則依序找
//
//	<執行檔>/../../office3d(console/bin/omixor-web → repo/office3d)、<執行檔>/../office3d、<執行檔>/office3d、
//	<工作目錄>/office3d、<工作目錄>/../office3d
//
// 有 places.json 才算找到。找不到時 API 回 503、靜態檔回 404,並記 log(不影響 console 啟動)。

// ErrNoDir 找不到 office3d 資料夾
var ErrNoDir = errors.New("找不到 office3d 資料夾(設定 OMIXOR_OFFICE_DIR,或把 office3d/ 放在 repo 最外層)")

// AgentInfo places.json 的 agents[]
type AgentInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Seat    string   `json:"seat"`
	Zone    string   `json:"zone"`
}

// PlaceInfo places.json 的 places[](只取需要的欄位)
type PlaceInfo struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	Owner   string    `json:"owner,omitempty"`
	Aliases []string  `json:"aliases"`
	Pos     []float64 `json:"position"`
}

// Catalog 角色與地點(places.json)
type Catalog struct {
	Agents []AgentInfo
	Places []PlaceInfo
	p      *parser
}

func (c *Catalog) agent(id string) *AgentInfo {
	for i := range c.Agents {
		if c.Agents[i].ID == id {
			return &c.Agents[i]
		}
	}
	return nil
}

func (c *Catalog) place(id string) *PlaceInfo {
	for i := range c.Places {
		if c.Places[i].ID == id {
			return &c.Places[i]
		}
	}
	return nil
}

func parseCatalog(b []byte) (*Catalog, error) {
	var raw struct {
		Agents []AgentInfo `json:"agents"`
		Places []PlaceInfo `json:"places"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("places.json 格式錯誤:%w", err)
	}
	c := &Catalog{}
	for _, a := range raw.Agents {
		if a.ID == "" {
			continue
		}
		if a.Name == "" {
			a.Name = a.ID
		}
		c.Agents = append(c.Agents, a)
	}
	for _, p := range raw.Places {
		if p.ID == "" {
			continue
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		c.Places = append(c.Places, p)
	}
	if len(c.Agents) == 0 || len(c.Places) == 0 {
		return nil, errors.New("places.json 沒有角色或地點")
	}
	c.p = newParser(c.Agents, c.Places)
	return c, nil
}

// candidateDirs 預設要找的位置(不含環境變數)
func candidateDirs() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		d := filepath.Dir(exe)
		out = append(out, filepath.Join(d, "..", "..", "office3d"), filepath.Join(d, "..", "office3d"), filepath.Join(d, "office3d"))
	}
	if wd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(wd, "office3d"), filepath.Join(wd, "..", "office3d"))
	}
	return out
}

func validDir(d string) bool {
	st, err := os.Stat(filepath.Join(d, "places.json"))
	return err == nil && st.Mode().IsRegular()
}

// ResolveDir 依 OMIXOR_OFFICE_DIR 或預設位置找 office3d 資料夾
func ResolveDir() (string, error) {
	if d := os.Getenv("OMIXOR_OFFICE_DIR"); d != "" {
		abs, err := filepath.Abs(d)
		if err == nil && validDir(abs) {
			return abs, nil
		}
		return "", fmt.Errorf("OMIXOR_OFFICE_DIR=%s 裡沒有 places.json", d)
	}
	for _, d := range candidateDirs() {
		if validDir(d) {
			abs, err := filepath.Abs(d)
			if err != nil {
				continue
			}
			return abs, nil
		}
	}
	return "", ErrNoDir
}

// dirSource 找資料夾並快取;找不到時每 10 秒才重找一次,log 只在狀態改變時記
type dirSource struct {
	mu       sync.Mutex
	fixed    string // 測試或呼叫端指定
	dir      string
	lastErr  error
	triedAt  time.Time
	logged   bool
	catalog  *Catalog
	catMod   time.Time
	catSize  int64
	catErr   error
	logf     func(string, ...any)
	retryGap time.Duration
}

func (s *dirSource) logfn(format string, a ...any) {
	if s.logf != nil {
		s.logf(format, a...)
		return
	}
	log.Printf(format, a...)
}

// Dir office3d 資料夾
func (s *dirSource) Dir() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirLocked()
}

func (s *dirSource) dirLocked() (string, error) {
	if s.fixed != "" {
		if !validDir(s.fixed) {
			return "", fmt.Errorf("%s 裡沒有 places.json", s.fixed)
		}
		return s.fixed, nil
	}
	if s.dir != "" && validDir(s.dir) {
		return s.dir, nil
	}
	if s.dir == "" && !s.triedAt.IsZero() && time.Since(s.triedAt) < s.retryGap {
		return "", s.lastErr
	}
	s.triedAt = time.Now()
	d, err := ResolveDir()
	if err != nil {
		s.dir, s.lastErr = "", err
		if !s.logged {
			s.logfn("虛擬辦公室:%v;/office/ 回 404、/api/office/command 回 503", err)
			s.logged = true
		}
		return "", err
	}
	if s.dir != d {
		s.logfn("虛擬辦公室:%s", d)
	}
	s.dir, s.lastErr, s.logged = d, nil, false
	return d, nil
}

// Catalog places.json(檔案改了會重讀)
func (s *dirSource) Catalog() (*Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dirLocked()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(d, "places.json")
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if s.catalog != nil && st.ModTime().Equal(s.catMod) && st.Size() == s.catSize {
		return s.catalog, nil
	}
	if s.catErr != nil && st.ModTime().Equal(s.catMod) && st.Size() == s.catSize {
		return nil, s.catErr
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	s.catMod, s.catSize = st.ModTime(), st.Size()
	c, err := parseCatalog(b)
	if err != nil {
		s.catalog, s.catErr = nil, err
		s.logfn("虛擬辦公室:%v", err)
		return nil, err
	}
	s.catalog, s.catErr = c, nil
	return c, nil
}
