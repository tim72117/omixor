package server

import "omixor-core/config"

// dbErr 已遮蔽機密的資料庫錯誤:訊息不含連線設定裡的密碼,原始錯誤仍可用 errors.Is / errors.As 比對
type dbErr struct {
	msg string
	err error
}

func (e *dbErr) Error() string { return e.msg }
func (e *dbErr) Unwrap() error { return e.err }

// redactDBErr 把 err 訊息裡的資料庫連線密碼(網址或 key=value 形式)換成 ***。
// store.NewPG 以 %w 包 pgx 的錯誤,訊息可能帶出連線設定;omixor-local 的連線設定可能來自 OMIXOR_DATABASE_URL
func redactDBErr(dbURL string, err error) error {
	if err == nil {
		return nil
	}
	cfg := config.Current().With(config.DatabaseURL, dbURL)
	return &dbErr{msg: cfg.Redact(err.Error()), err: err}
}
