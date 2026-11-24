package oauth

import (
	"errors"
	"net/http"

	"omixor-core/service"
)

// PairProvider 配對碼(在已登入的 omixor 的「手機配對」產生的一次性碼):規則(全域鎖定、頻率限制、只用一次)在 service.ProveOAuthPair
type PairProvider struct{ Svc *service.Service }

const pairField = "pairing_code"

func (p *PairProvider) ID() string    { return "pair" }
func (p *PairProvider) Label() string { return "配對碼" }
func (p *PairProvider) Fields() []Field {
	return []Field{{Name: pairField, Kind: "text", Label: "配對碼(在已登入的 omixor 的「手機配對」產生,5 分鐘內有效、只能用一次)",
		Placeholder: "XXXX-XXXX", MaxLen: 16, Caps: true}}
}
func (p *PairProvider) FormActions() []string { return []string{"action=allow"} }
func (p *PairProvider) Present() Presentation {
	return Presentation{Button: "用配對碼允許", SoloButton: "允許", Summary: "改用配對碼"}
}
func (p *PairProvider) Callback() http.Handler { return nil }

// Start 用配對碼證明身分:碼不對(一律同一句話,不說是不存在、過期或用過)重新顯示同意畫面;鎖定、帳號停用等照 OAuthError 顯示
func (p *PairProvider) Start(w http.ResponseWriter, r *http.Request, t Txn) (Outcome, error) {
	u, extra, err := p.Svc.ProveOAuthPair(t.Checked, t.Form.Get(pairField), service.ClientIP(r))
	if err != nil {
		var oe *service.OAuthError
		if errors.As(err, &oe) && oe == service.ErrOAuthPairInvalid {
			return retryOutcome(r, oe, "wrong"), nil
		}
		return Outcome{}, err
	}
	return Outcome{Done: &Identity{User: u, Extra: extra, Hint: "若用配對碼,請重新產生一個。"}}, nil
}
