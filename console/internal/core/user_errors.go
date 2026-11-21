package core

import (
	"errors"

	"omixor-core/errmask"
	"omixor-core/httperr"
)

// 給畫面的錯誤訊息:不要把任意 err.Error() 直接包成 UserError(UserError 是「刻意給人看的」,出口不會遮蔽,
// 資料庫文字、檔案路徑、連線位址、遠端伺服器的回應就會原樣出現在每個登入的人的畫面)。
// 規則:
//   - 已經是 httperr.Public(UserError、StatusError、核心的 *service.Error 等):原樣回傳(它們自己決定狀態碼與訊息)
//   - Graph API 的 FBError(Meta 回的說明文字):文字不含內部細節時當使用者訊息;含的走遮蔽
//   - 其他一律不包,原樣往上丟,由出口(web 的 httperr.Report)分類:內部細節換成通用訊息、原文進 log

// facing err 要回給畫面時:是已知「給人看」的型別就保留,其餘原樣丟給出口分類遮蔽
func facing(err error) error {
	if err == nil {
		return nil
	}
	var fb FBError
	if errors.As(err, &fb) {
		if _, masked := errmask.Mask(errors.New(err.Error()), true); masked {
			return errors.New(err.Error()) // 文字含內部細節:丟掉型別(FBError 本身是 Public,不會被遮蔽),讓出口分類遮蔽
		}
		return UserError{err.Error()}
	}
	var pub httperr.Public
	if errors.As(err, &pub) {
		return err
	}
	return err
}

// facingPrefix 同 facing,但要在訊息前面加一段說明(例如「Issue 沒有存起來:」):
// 先照 httperr 分類(遮蔽的原文進 log),再把說明加在對外訊息前面;狀態碼與 data 沿用分類結果(沒指定時 400)
func facingPrefix(op, prefix string, err error) error {
	if err == nil {
		return nil
	}
	in := httperr.Report(op, err)
	return StatusError{Status: in.StatusOr(400), Message: prefix + in.Msg, Data: in.Data}
}
