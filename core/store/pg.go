// pg.go PG 結構與連線、NewPG 與跨領域共用的輔助函式(建表語句與資料表說明在 pg_schema.go)

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/model"
)

// PostgreSQL 的存放處。建表語句與資料表說明見 pg_schema.go。

// pgConn PG 用的資料庫連線:連線池(一般)或批次裡的交易(Batch)。交易的 Begin 是 savepoint,
// 所以方法裡自己開的交易在批次裡就是巢狀交易,批次失敗時一起還原
type pgConn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type PG struct {
	pool pgConn
	root *pgxpool.Pool // 連線池本身(Close 用;批次裡的 PG 是 nil)
}

// txConn 批次裡的交易:BeginTx 在交易裡只能開 savepoint(隔離等級沿用批次的)
type txConn struct{ pgx.Tx }

func (t txConn) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) { return t.Tx.Begin(ctx) }

// Batch 批次寫入(全有或全無):fn 用 tx 做的寫入在同一個資料庫交易裡,fn 成功才一起 commit,
// 回傳錯誤(或 panic)就整批還原。fn 裡只能用 tx(用 p 的寫入不在這個交易裡)。批次以外的讀寫不受影響
// (讀不到批次沒 commit 的資料;同一個組織的 Issue、任務編號用 advisory lock,會等到批次結束)
func (p *PG) Batch(fn func(tx *PG) error) error {
	if p.root == nil { // 已經在批次裡:直接用同一個交易
		return fn(p)
	}
	c, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tx, err := p.root.Begin(c)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(c) }() // commit 之後是 no-op
	if err := fn(&PG{pool: txConn{tx}}); err != nil {
		return err
	}
	return tx.Commit(c)
}

// newPG 用連線池組出 *PG(NewPG、NewPGFromPool、AttachMigratedPG 共用這一處;PG 日後加欄位只改這裡)
func newPG(pool *pgxpool.Pool) *PG {
	return &PG{pool: pool, root: pool}
}

// NewPG 連資料庫並建表(全新資料庫建出最終結構;已有的資料庫套用還沒做的結構異動步驟,見 bootstrapSchema)
func NewPG(url string) (*PG, error) {
	c, cancel := ctx()
	defer cancel()
	pool, err := pgxpool.New(c, url)
	if err != nil {
		return nil, fmt.Errorf("資料庫網址格式不對:%w", err)
	}
	if err := pool.Ping(c); err != nil {
		pool.Close()
		return nil, fmt.Errorf("連不到資料庫:%w", err)
	}
	bc, bcancel := bootstrapCtx() // 建表與結構異動用較長的逾時,不受 15 秒的 ctx() 限制
	defer bcancel()
	if err := bootstrapSchema(bc, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return newPG(pool), nil
}

// NewPGFromPool 用已經連好的連線池(omixor-web 內建模式和它自己的資料共用一個)
func NewPGFromPool(pool *pgxpool.Pool) (*PG, error) {
	c, cancel := bootstrapCtx()
	defer cancel()
	if err := bootstrapSchema(c, pool); err != nil {
		return nil, err
	}
	return newPG(pool), nil
}

func (p *PG) Close() {
	if p.root != nil {
		p.root.Close()
	}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func secs(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// utcTime 資料庫讀出的時間 → model.Time(UTC)
func utcTime(t time.Time) model.Time { return model.Time{Time: t.UTC()} }

// fromNull 可為 NULL 的時間 → model.Time(NULL 是零值)
func fromNull(t *time.Time) model.Time {
	if t == nil {
		return model.Time{}
	}
	return utcTime(*t)
}

// tp 可為 NULL 的時間 → *model.Time(NULL 是 nil)
func tp(t *time.Time) *model.Time {
	if t == nil {
		return nil
	}
	x := utcTime(*t)
	return &x
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func jb(v any) []byte { b, _ := json.Marshal(v); return b }

func optSecs(t *model.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	x := secs(t.Time)
	return &x
}

// nullSecs 零值時間存 NULL(其餘截到秒、UTC)
func nullSecs(t model.Time) *time.Time { return optSecs(&t) }

func tagsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
