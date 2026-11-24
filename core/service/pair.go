package service

import (
	"crypto/rand"
	"errors"
	"log"
	"math/big"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// 手機配對:已登入的裝置(Mac App)要一組一次性配對碼,手機拿配對碼換一把自己的 omixor token。
//
//   - 配對碼:8 個字元,字母表去掉易混的 0 O 1 I L(31 個字元,約 39 bits);顯示成 XXXX-XXXX,輸入時不分大小寫、忽略空白與 -
//   - 效期 PairTTL(5 分鐘),用過一次就失效
//   - 只存雜湊:借用 token 表存一列(id 是 pair_…,雜湊是 HashToken("pair:"+碼)、expiresAt 是效期)。
//     Authenticate 只收 omx_ 開頭的原文、雜湊領域也不同,所以這列不能拿來當登入用;
//     換 token 時用 RevokeToken 標成用過(撤銷是原子的,多個實例同時換也只有一個成功)
//   - 換 token 不用登入,有自己的頻率限制(每個來源、整個入口,與登入互不影響);失敗一律回同一句話,不洩漏碼存不存在、過期或用過
const (
	PairTTL        = 5 * time.Minute
	PairCodeLength = 8
	PairAlphabet   = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	pairIDPrefix   = "pair_"
	pairDevice     = "手機配對碼"
)

// PairCode 新的配對碼(Code 是原文,只在這時候給一次)
type PairCode struct {
	Code      string     `json:"code"`
	ExpiresAt model.Time `json:"expiresAt"`
}

func newPairCode() string {
	b := make([]byte, PairCodeLength)
	max := big.NewInt(int64(len(PairAlphabet)))
	for i := range b {
		n, _ := rand.Int(rand.Reader, max)
		b[i] = PairAlphabet[n.Int64()]
	}
	return string(b)
}

// NormalizePairCode 去掉空白與 -、轉大寫;格式不對回空字串
func NormalizePairCode(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch {
		case r == ' ' || r == '-' || r == '\t' || r == '\n':
			continue
		case strings.ContainsRune(PairAlphabet, r):
			sb.WriteRune(r)
		default:
			return ""
		}
	}
	if sb.Len() != PairCodeLength {
		return ""
	}
	return sb.String()
}

func pairHash(code string) []byte { return HashToken("pair:" + code) }

// IsPairToken token 表裡的這一列是配對碼(不是登入用的 token)
func IsPairToken(t model.Token) bool { return strings.HasPrefix(t.ID, pairIDPrefix) }

// CreatePairCode 已登入的使用者產生一次性配對碼(角色憑證不行)
func (s *Service) CreatePairCode(p *model.Principal) (*PairCode, error) {
	if p == nil || p.Agent != nil || p.User.ID == "" {
		return nil, fail(403, "要用使用者的登入才能配對手機")
	}
	if IsPairToken(p.Token) {
		return nil, fail(403, "配對碼不能拿來登入")
	}
	if p.Via == model.ViaOAuth { // OAuth 連線(含 mcp:write)不能產生配對碼:否則可以自己再授權、換出登入 token
		return nil, fail(403, "OAuth 連線不能產生配對碼")
	}
	now := s.mt()
	code := newPairCode()
	exp := model.Time{Time: now.Add(PairTTL)}
	t := model.Token{ID: RandID(pairIDPrefix, 9), UserID: p.User.ID, Device: pairDevice, CreatedAt: now, ExpiresAt: &exp, Hash: pairHash(code)}
	if err := s.Store.CreateToken(t); err != nil {
		return nil, err
	}
	s.Audit(p, "pair.create", "", map[string]any{"pair": t.ID, "from": p.Token.ID})
	return &PairCode{Code: code, ExpiresAt: exp}, nil
}

var errPairDisabled = &Error{Code: 403, Msg: "這個帳號已停用"}
var errPairInvalid = &Error{Code: 401, Msg: "配對碼無效、已過期或已用過,請在 Mac App 重新產生"}

// redeemPair 驗證並用掉一組配對碼(有頻率限制;一次性):回傳那一列與它的使用者。使用者停用時回 errPairDisabled(碼不消耗,u 仍有值,供呼叫者寫稽核)。
// ClaimPairCode(換登入 token)與 OAuth 同意畫面(證明身分,不發登入 token)共用,規則完全相同
func (s *Service) redeemPair(code, ip string) (*model.Token, *model.User, error) {
	if !s.allowLogin(&s.logins.pair, ip) {
		return nil, nil, fail(429, "配對嘗試太頻繁,請一分鐘後再試")
	}
	code = NormalizePairCode(code)
	if code == "" {
		return nil, nil, errPairInvalid
	}
	now := s.now()
	pt, u, err := s.Store.TokenByHash(pairHash(code), now)
	if err != nil || pt == nil || u == nil || !IsPairToken(*pt) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
		s.Audit(nil, "pair.denied", "", map[string]any{"ip": ip})
		return nil, nil, errPairInvalid
	}
	// 停用的帳號:在消耗配對碼之前就擋(碼保留,帳號啟用後還能用)
	if u.Disabled {
		return pt, u, errPairDisabled
	}
	// 標成用過:撤銷是原子的,同一個碼只有一次成功。
	// 找不到(= 已被別人用掉或撤銷)一定要當失敗:一次性靠的就是只有一個請求撤銷成功;其他資料庫錯誤是伺服器的問題,回 500 並記下來
	if err := s.Store.RevokeToken(pt.ID, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, errPairInvalid
		}
		log.Printf("配對碼標成用過失敗(%s):%v", pt.ID, err)
		return nil, nil, fail(500, "暫時無法處理配對,請稍後再試")
	}
	return pt, u, nil
}

// ClaimPairCode 手機用配對碼換一把新的 omixor token(不用登入;有頻率限制;一次性)
func (s *Service) ClaimPairCode(code, device, ip string) (*LoginResult, error) {
	pt, u, err := s.redeemPair(code, ip)
	if err != nil {
		if err == errPairDisabled {
			s.Audit(&model.Principal{User: *u}, "pair.denied", "", map[string]any{"reason": "disabled"})
		}
		return nil, err
	}
	device = strings.TrimSpace(device)
	if device == "" {
		device = "手機"
	}
	if r := []rune(device); len(r) > 80 {
		device = string(r[:80])
	}
	mnow := s.mt()
	raw := RandID(TokenPrefix, 32)
	t := model.Token{ID: RandID("t_", 9), UserID: u.ID, Device: device, CreatedAt: mnow, Hash: HashToken(raw)}
	if s.TTL > 0 {
		exp := model.Time{Time: mnow.Add(s.TTL)}
		t.ExpiresAt = &exp
	}
	if err := s.Store.CreateToken(t); err != nil {
		return nil, err
	}
	s.Audit(&model.Principal{User: *u, Token: t}, "pair.claim", "", map[string]any{"token": t.ID, "pair": pt.ID, "device": device})
	return &LoginResult{Token: raw, TokenID: t.ID, ExpiresAt: t.ExpiresAt, User: *u}, nil
}
