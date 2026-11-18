package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// 舊資料庫(文件櫃結構主鍵是 org、文件沒有 project 欄位)升級:建表時補欄位、主鍵換成 (org, project),
// 舊資料變成「未分專案」;再跑一次建表不會再動(冪等)
func TestPGProjectMigration(t *testing.T) {
	url := testPGURL(t)
	ctx := context.Background()
	schema := fmt.Sprintf("core_mig_%d", time.Now().UnixNano())
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = c.Exec(ctx, "drop schema "+schema+" cascade")
		c.Close(ctx)
	}()
	for _, q := range []string{
		"create schema " + schema,
		"set search_path = " + schema,
		`create table omixor_cabinet_layouts (org text primary key, layout jsonb not null default '[]', updated_at timestamptz not null default now())`,
		`create table omixor_docs (org text not null, id text not null, number integer not null, pos bigint not null, title text not null,
		  body text not null default '', section text not null, tab text, tags jsonb not null default '[]', author text not null default '',
		  created_at timestamptz not null, updated_at timestamptz not null, primary key (org, id))`,
		`insert into omixor_cabinet_layouts (org, layout) values ('o', '[{"id":"old","label":"舊的","path":"","tabs":[]}]')`,
		`insert into omixor_docs (org, id, number, pos, title, section, created_at, updated_at) values ('o', 'DOC-1', 1, 1, '舊文件', 'old', now(), now())`,
	} {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	for round := 1; round <= 2; round++ { // 第二次:已經升級過,建表不出錯也不動資料
		p, err := NewPG(url + sep + "search_path=" + schema)
		if err != nil {
			t.Fatal("第", round, "次建表", err)
		}
		l, err := p.GetCabinetLayout("o", "")
		if err != nil || len(l) != 1 || l[0].ID != "old" {
			t.Fatal("舊結構要變成未分專案的", round, l, err)
		}
		d, err := p.GetDoc("o", "DOC-1")
		if err != nil || d.Project != "" || d.Title != "舊文件" {
			t.Fatal("舊文件要是未分專案", round, d, err)
		}
		if round == 1 {
			if err := p.SaveCabinetLayout("o", "PRJ-1", []model.CabinetSection{{ID: "new", Label: "新", Tabs: []model.CabinetTab{}}}); err != nil {
				t.Fatal("同組織存專案的結構(主鍵要是 (org, project))", err)
			}
		}
		if l, err := p.GetCabinetLayout("o", "PRJ-1"); err != nil || len(l) != 1 || l[0].ID != "new" {
			t.Fatal("專案的結構", round, l, err)
		}
		p.Close()
	}
	var cols int
	if err := c.QueryRow(ctx, `select indnatts from pg_index where indrelid = 'omixor_cabinet_layouts'::regclass and indisprimary`).Scan(&cols); err != nil || cols != 2 {
		t.Fatal("主鍵要是 (org, project)", cols, err)
	}
}
