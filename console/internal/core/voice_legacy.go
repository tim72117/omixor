package core

import "path/filepath"

// 舊版把旁白的專案路徑、發佈連線、bucket 寫死當預設值;現在預設都是空白。
// 這裡讓原本靠這些預設值在用的安裝升級後照舊:第一次啟動時,看得出用過(有批次紀錄,或專案資料夾裡有腳本資料夾)
// 就把空白的欄位填成舊的值並存檔;已經填的值不動。只做一次,全新安裝什麼都不填。
const (
	voiceLegacyShuttlePath = "~/Documents/shuttle"
	voiceLegacyConnection  = "evoleap"
	voiceLegacyBucket      = "shuttle-voice"
)

// keepLegacyDefaults 見檔頭(啟動時在 load 之後呼叫,拿著 Mu)
func (p *VoicePipeline) keepLegacyDefaults() {
	if p.Settings.LegacyChecked {
		return
	}
	saved := p.Settings
	if p.Settings.ShuttlePath == "" {
		p.Settings.ShuttlePath = voiceLegacyShuttlePath
	}
	used := len(p.Jobs) > 0 || Exists(filepath.Clean(p.liveDir()))
	if !used {
		p.Settings = saved
	} else {
		if p.Settings.Connection == "" {
			p.Settings.Connection = voiceLegacyConnection
		}
		if p.Settings.Bucket == "" {
			p.Settings.Bucket = voiceLegacyBucket
		}
	}
	p.Settings.LegacyChecked = true
	p.SaveSettings()
}
