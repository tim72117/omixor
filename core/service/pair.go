package service

import (
	"crypto/rand"
	"errors"
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
//   - 換 token 不用登入,跟 evoleap 登入共用頻率限制;失敗一律回同一句話,不洩漏碼存不存在、過期或用過
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

var errPairInvalid = &Error{Code: 401, Msg: "配對碼無效、已過期或已用過,請在 Mac App 重新產生"}

// ClaimPairCode 手機用配對碼換一把新的 omixor token(不用登入;有頻率限制;一次性)
func (s *Service) ClaimPairCode(code, device, ip string) (*LoginResult, error) {
	if !s.allowLogin(ip) {
		return nil, fail(429, "配對嘗試太頻繁,請一分鐘後再試")
	}
	code = NormalizePairCode(code)
	if code == "" {
		return nil, errPairInvalid
	}
	now := s.now()
	pt, u, err := s.Store.TokenByHash(pairHash(code), now)
	if err != nil || pt == nil || u == nil || !IsPairToken(*pt) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		s.Audit(nil, "pair.denied", "", map[string]any{"ip": ip})
		return nil, errPairInvalid
	}
	// 標成用過:撤銷是原子的,同一個碼只有一次成功
	if err := s.Store.RevokeToken(pt.ID, now); err != nil {
		return nil, errPairInvalid
	}
	if u.Disabled {
		s.Audit(&model.Principal{User: *u}, "pair.denied", "", map[string]any{"reason": "disabled"})
		return nil, fail(403, "這個帳號已停用")
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
