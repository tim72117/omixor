package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 步驟 4:既有組織沒存過值的旁白發佈、網站訪客寫成開;存過的值(含 false)與其他 key 不動;重跑不變。
func TestKeepVoiceVisitorsForExistingOrgs(t *testing.T) {
	url := testPGBlank(t)
	p, err := NewPG(url)
	chk(t, err)
	t.Cleanup(p.Close)
	c := context.Background()
	var pool *pgxpool.Pool = p.root
	_, err = pool.Exec(c, `insert into omixor_orgs (id, name) values ('plain', 'a'), ('chosen', 'b');
		insert into omixor_org_system (org, features, updated_at) values ('chosen', '{"pipelines.voice": false, "channels": false}', '2026-01-01T00:00:00Z')`)
	chk(t, err)
	for range 2 {
		_, err = pool.Exec(c, keepVoiceVisitorsSQL)
		chk(t, err)
	}
	x, err := p.GetSystemSettings("plain")
	chk(t, err)
	if len(x.Features) != 2 || !x.Features["pipelines.voice"] || !x.Features["monitoring.visitors"] {
		t.Fatalf("沒存過的組織:%+v", x.Features)
	}
	y, err := p.GetSystemSettings("chosen")
	chk(t, err)
	if len(y.Features) != 3 || y.Features["pipelines.voice"] || y.Features["channels"] || !y.Features["monitoring.visitors"] {
		t.Fatalf("存過的值要保留:%+v", y.Features)
	}
}
