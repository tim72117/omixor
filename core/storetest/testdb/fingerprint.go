package testdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/jackc/pgx/v5"
)

// fingerprintSQL 結構指紋要比對的各段(每段一個查詢、固定排序):使用者的表/序號/檢視、欄位(型別、NOT NULL、預設值、產生欄位表達式)、
// 約束(含外鍵、是否已 validate)、索引、觸發器。資料與序號目前值不在其中(那些由回收時的 truncate 還原)。
var fingerprintSQL = []string{
	// 關聯表(r 表、p 分割表、v 檢視、m 實體化檢視、S 序號、f 外部表)
	`select coalesce(string_agg(format('%s.%s:%s', n.nspname, c.relname, c.relkind), E'\n' order by n.nspname, c.relname), '')
	   from pg_class c join pg_namespace n on n.oid = c.relnamespace
	  where n.nspname not in ('pg_catalog', 'information_schema') and n.nspname not like 'pg_toast%' and n.nspname not like 'pg_temp%'
	    and c.relkind in ('r', 'p', 'v', 'm', 'S', 'f')`,
	// 欄位(含 generated 欄位的表達式)
	`select coalesce(string_agg(format('%s.%s.%s:%s:%s:%s:%s:%s', n.nspname, c.relname, a.attname, format_type(a.atttypid, a.atttypmod),
	          a.attnotnull, a.attgenerated, a.attidentity, coalesce(pg_get_expr(d.adbin, d.adrelid), '')), E'\n' order by n.nspname, c.relname, a.attname), '')
	   from pg_attribute a join pg_class c on c.oid = a.attrelid join pg_namespace n on n.oid = c.relnamespace
	   left join pg_attrdef d on d.adrelid = a.attrelid and d.adnum = a.attnum
	  where a.attnum > 0 and not a.attisdropped and c.relkind in ('r', 'p', 'v', 'm', 'f')
	    and n.nspname not in ('pg_catalog', 'information_schema') and n.nspname not like 'pg_toast%' and n.nspname not like 'pg_temp%'`,
	// 約束
	`select coalesce(string_agg(format('%s.%s:%s:%s:%s', n.nspname, c.relname, k.conname, pg_get_constraintdef(k.oid), k.convalidated), E'\n'
	          order by n.nspname, c.relname, k.conname), '')
	   from pg_constraint k join pg_class c on c.oid = k.conrelid join pg_namespace n on n.oid = c.relnamespace
	  where n.nspname not in ('pg_catalog', 'information_schema') and n.nspname not like 'pg_toast%'`,
	// 索引
	`select coalesce(string_agg(format('%s.%s.%s:%s', schemaname, tablename, indexname, indexdef), E'\n' order by schemaname, tablename, indexname), '')
	   from pg_indexes where schemaname not in ('pg_catalog', 'information_schema')`,
	// 觸發器(不含約束自動產生的內部觸發器)
	`select coalesce(string_agg(format('%s:%s', tgrelid::regclass::text, pg_get_triggerdef(t.oid)), E'\n' order by tgrelid::regclass::text, tgname), '')
	   from pg_trigger t where not tgisinternal`,
}

// structureMetaTables 「結構中繼資料表」:遷移時會寫入固定資料列(目前是 omixor_actors 的系統 actor;測試用 fp_meta、fp_keep 驗證機制),
// 所以範本不是全空;範本空檢查放行、回收時不 truncate,結構指紋則把它們的固定資料列一起算進去(測試改了這些列就視為污染,丟棄不回收)。
// 新增這類表時(遷移寫了固定列的系統表)要加進來,並在 core/store 的設計裡維持「冪等、同一版本內不再改列」。
//
// 只有一部分列是固定的(表裡也會有測試自己建的列,例如 omixor_actors 的使用者、角色)時,在 metaTableKeep 登記「固定列」的條件:
// 條件以外的列不算結構(範本裡不能有、指紋不算、回收時刪掉);登記了條件的表,測試建的列因此不會害資料庫被丟棄。
var structureMetaTables = []string{"omixor_actors"}

// metaTableKeep 結構中繼資料表 → 固定列的 SQL 條件(省略 = 整張表都是固定的)。
// omixor_actors:遷移寫入的只有 kind = 'system' 的固定系統 actor(model.SystemActors);使用者、角色、以及資料裡舊字串對不上時自動建的
// sys_legacy_*(「誰」欄位的過渡橋與寬鬆解析器,見 store/pg_actor_bridge.go)是測試或服務建的資料,回收時刪掉
var metaTableKeep = map[string]string{"omixor_actors": "kind = 'system' and left(id, 11) <> 'sys_legacy_'"}

// metaKeepWhere 固定列的 where 子句(含前導空白);沒登記條件回 ""
func metaKeepWhere(t string) string {
	if k := metaTableKeep[t]; k != "" {
		return " where " + k
	}
	return ""
}

func isMetaTable(qualified string) bool {
	for _, t := range structureMetaTables {
		if qualified == "public."+t || qualified == `"public"."`+t+`"` {
			return true
		}
	}
	return false
}

// fingerprint 算資料庫的結構指紋(sha256 hex)。同一個範本複製出來、沒被測試改過結構的資料庫指紋一致
func fingerprint(ctx context.Context, c *pgx.Conn) (string, error) {
	h := sha256.New()
	for i, q := range fingerprintSQL {
		var s string
		if err := c.QueryRow(ctx, q).Scan(&s); err != nil {
			return "", err
		}
		h.Write([]byte{byte(i), '\n'})
		h.Write([]byte(s))
		h.Write([]byte{'\n'})
	}
	for _, t := range structureMetaTables {
		var s string
		var exists bool
		if err := c.QueryRow(ctx, `select to_regclass($1) is not null`, "public."+t).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			continue
		}
		if err := c.QueryRow(ctx, `select coalesce(string_agg(m::text, E'\n' order by m::text), '') from public.`+pgx.Identifier{t}.Sanitize()+` m`+metaKeepWhere(t)).Scan(&s); err != nil {
			return "", err
		}
		h.Write([]byte("meta:" + t + "\n" + s + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
