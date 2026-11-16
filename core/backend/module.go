package backend

// 給選用模組(core/module、core/modules/…)用的公開工具:模組在自己的套件裡定義自己的介面與 Local / Remote 實作
// (例如 core/modules/integrations 的 Backend 與 Of),用這些和核心的 Local、Remote 行為一致。

// ErrUnsupported 這個模式不支援這個操作(同 Unsupported 回的錯誤)
var ErrUnsupported = errUnsupported

// PrepareOrg 操作前的準備:EnsureOrg 時組織還沒建就補建(同核心 Local 的其他操作)
func (l *Local) PrepareOrg(org string) { l.ensureOrg(org) }

// ServerOrg 呼叫端的組織 id 換成伺服器上的 id(OrgID 是 nil 時原樣)
func (r *Remote) ServerOrg(org string) string { return r.org(org) }

// Wrap 伺服器回的錯誤轉成 *service.Error(同核心 Remote 的其他操作)
func Wrap(err error) error { return wrap(err) }
