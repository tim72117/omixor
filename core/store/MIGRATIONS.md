# 資料庫遷移說明:jsonb 欄位拆成資料表(PG)

這一輪把原本塞在 jsonb 欄位裡的陣列、物件拆成有外鍵的資料表(對外的 model、Store 介面不變)。測試一律走 PostgreSQL(`core/storetest`)。

> 舊的 File store(`store.json`)已完全移除:舊資料夾不再被讀取,也**沒有遷移工具**(使用者決定不遷移)。`omixor-web import-issues` 是另一條路徑(把組織資料夾裡的 `issues.json` 匯入 Issue 表),與此無關。

## 1. 不可回滾

**這一輪遷移不支援回滾到舊版。**

- 新版不再寫舊的 jsonb 欄位(不雙寫):新建的列,舊欄位是欄位預設值(`'[]'`、`'{}'`);已展開的列,舊欄位停在升級那一刻。
- 回到舊版:新版期間新增、修改的資料(標籤、成員、提及、備註、事件內容、文件櫃結構、例行任務等)舊版看不到;再升級一次時,已展開的列(`split_at` 非 NULL)以新表為權威,舊版期間的寫入會被忽略。
- 所以**要回滾只能從升級前的備份還原**。升級前請先備份(`pg_dump`)。
- 舊欄位與 `split_at` 欄位這個版本**不 drop**,只是為了讓遷移可以分批、可以重跑、遷移沒做完時讀取能退回舊資料,不是為了回滾。
- 下個版本才 drop 舊欄位(第 5 節)。**升級路徑不得跳過「遷移還在」的這個版本**(例如不能從更舊的版本直接升到 drop 欄位的版本,否則還沒展開的列會永遠丟失)。

## 2. 本輪新增的表與欄位

新表(主鍵、外鍵、`on delete cascade` 的細節見各 `pg_split_*.go` 檔頭):

| 表 | 取代的舊欄位 | 說明 |
|---|---|---|
| `omixor_channel_members` | `omixor_channels.members` | 頻道成員,`pos` 為順序 |
| `omixor_channel_mentions` | `omixor_channel_messages.mentions` | 訊息提及 |
| `omixor_org_chain_edges` | `omixor_org_chains.edges` | 組織鏈的邊 |
| `omixor_doc_tags` | `omixor_docs.tags` | 文件標籤 |
| `omixor_agent_doc_access` | `omixor_agents.doc_access` | 角色的文件權限 |
| `omixor_agent_routines` | `omixor_agents.routines` | 角色的例行任務 |
| `omixor_agent_task_notes`、`omixor_agent_task_links` | `omixor_agent_tasks.notes`、`.links` | 任務備註與連結 |
| `omixor_incident_services`、`_follow_ups`、`_alerts`、`_events` | `omixor_incidents.data` | 事件的清單與時間軸 |
| `omixor_cabinet_sections`、`omixor_cabinet_tabs` | `omixor_cabinet_layouts.layout` | 文件櫃結構 |

新欄位:

- 每張父表的 `split_at timestamptz`:NULL = 還沒展開到新表。涵蓋 `omixor_channels`、`omixor_channel_messages`、`omixor_org_chains`、`omixor_docs`、`omixor_agents`、`omixor_agent_tasks`、`omixor_incidents`、`omixor_cabinet_layouts`。
- `omixor_incidents` 的純量欄位:`title`、`sev`、`commander`、`resolved_at`、`impact`、`ai_summary`、`created_by`。
- `omixor_cabinet_layouts.version`(樂觀鎖版本號)。
- 部分索引 `<表>_unsplit`(只含 `split_at is null` 的列,啟動遷移判斷「還有沒有要做的」用,瞬間完成)。

## 3. 啟動遷移的行為

- 在 advisory lock(`omixor-split-migrate`)內進行;多個執行個體同時啟動時排隊,後到的發現沒東西要做就直接略過。
- 逾時 30 分鐘(變數 `migrateTimeout`,和一般查詢的 15 秒無關);每批 500 列(變數 `migrateBatchSize`),每批一個交易,批內每列一個 savepoint(壞資料只回滾那一列)。沒有子資料的列(空陣列等)用一個語句標記。
- 無損可修的資料遷移時直接修:成員、提及、標籤重複 → 去重(留第一個);例行任務 id 空白或重複 → 補新 uuid(第一個保留原 id,`omixor_agent_tasks.routine_id` 不受影響),`doc_access` 照常展開。
- 真的展開不了的列(JSON 壞掉、型別不符、外鍵不成立):略過,`split_at` 保持 NULL,記一行 `[遷移警告]` log;結束時再記一行彙總(各表未展開列數)。逾時或沒做完也只記 `[遷移警告]`,**不讓啟動失敗**,下次啟動接著做。
- 未展開的列:
  - 讀取:退回讀舊 jsonb(結構壞到解不出來的欄位回空;文件櫃解不出來回 `ErrUnmigratedRow`,不回空結構)。
  - 寫入:先在同一個交易裡嘗試展開,展開不了回 `ErrUnmigratedRow`(不靜默覆寫舊資料)。修好那一列的舊資料之後重啟(或直接再寫一次,寫入時會先展開)即可。
- 程式裡查未展開列數:`(*PG).UnmigratedRows()`、`UnmigratedRowsByTable()`(0 = 遷移完成,可以進下個版本)。

## 4. 上線前檢查 SQL

下面的查詢找出「遷移會略過(或需要修正)的列」。升級前在舊版資料庫上跑,有結果就先決定要怎麼處理(很多只是無損去重,遷移會自己處理)。

```sql
-- (a) JSON 結構不對(遷移略過、讀取退回舊資料、寫入被拒絕)
select 'docs.tags' as src, org, id from omixor_docs where jsonb_typeof(tags) <> 'array'
union all select 'channels.members', org, id from omixor_channels where jsonb_typeof(members) <> 'array'
union all select 'messages.mentions', org, channel_id || '/' || id from omixor_channel_messages where jsonb_typeof(mentions) <> 'array'
union all select 'org_chains.edges', org, org from omixor_org_chains where jsonb_typeof(edges) <> 'array'
union all select 'agents.doc_access', org, id from omixor_agents where jsonb_typeof(doc_access) <> 'object'
union all select 'agents.routines', org, id from omixor_agents where jsonb_typeof(routines) <> 'array'
union all select 'tasks.links', org, id from omixor_agent_tasks where jsonb_typeof(links) <> 'array'
union all select 'tasks.notes', org, id from omixor_agent_tasks where jsonb_typeof(notes) <> 'array'
union all select 'incidents.data', org, id from omixor_incidents where jsonb_typeof(data) <> 'object'
union all select 'cabinet.layout', org, project from omixor_cabinet_layouts where jsonb_typeof(layout) <> 'array';

-- (b) 元素型別不符(陣列裡有不是字串的元素)
select 'docs.tags' as src, org, id from omixor_docs
  where jsonb_typeof(tags) = 'array' and exists (select 1 from jsonb_array_elements(tags) e where jsonb_typeof(e) <> 'string')
union all select 'channels.members', org, id from omixor_channels
  where jsonb_typeof(members) = 'array' and exists (select 1 from jsonb_array_elements(members) e where jsonb_typeof(e) <> 'string')
union all select 'messages.mentions', org, channel_id || '/' || id from omixor_channel_messages
  where jsonb_typeof(mentions) = 'array' and exists (select 1 from jsonb_array_elements(mentions) e where jsonb_typeof(e) <> 'string');

-- (c) 重複值(遷移會自動去重或補 uuid,只是先知道有哪些)
select 'docs.tags' as src, org, id from omixor_docs
  where jsonb_typeof(tags) = 'array' and (select count(*) <> count(distinct e) from jsonb_array_elements_text(tags) e)
union all select 'channels.members', org, id from omixor_channels
  where jsonb_typeof(members) = 'array' and (select count(*) <> count(distinct e) from jsonb_array_elements_text(members) e)
union all select 'messages.mentions', org, channel_id || '/' || id from omixor_channel_messages
  where jsonb_typeof(mentions) = 'array' and (select count(*) <> count(distinct e) from jsonb_array_elements_text(mentions) e)
union all select 'agents.routines id 重複或空白', org, id from omixor_agents
  where jsonb_typeof(routines) = 'array'
    and (select count(*) <> count(distinct coalesce(r->>'id', '')) or bool_or(coalesce(r->>'id', '') = '') from jsonb_array_elements(routines) r);

-- (d) 文件櫃:區塊 id 或頁籤網址重複(遷移略過,讀取退回舊 layout)
select org, project from omixor_cabinet_layouts l
  where jsonb_typeof(layout) = 'array' and (
    (select count(*) <> count(distinct s->>'id') from jsonb_array_elements(layout) s)
    or (select count(*) <> count(distinct t->>'path') from jsonb_array_elements(layout) s, jsonb_array_elements(coalesce(s->'tabs', '[]'::jsonb)) t));
```

升級後檢查還剩多少未展開(應該是 0,或只剩上面 (a)(b)(d) 找到的列):

```sql
select 'omixor_channels' t, count(*) from omixor_channels where split_at is null
union all select 'omixor_channel_messages', count(*) from omixor_channel_messages where split_at is null
union all select 'omixor_org_chains', count(*) from omixor_org_chains where split_at is null
union all select 'omixor_docs', count(*) from omixor_docs where split_at is null
union all select 'omixor_agents', count(*) from omixor_agents where split_at is null
union all select 'omixor_agent_tasks', count(*) from omixor_agent_tasks where split_at is null
union all select 'omixor_incidents', count(*) from omixor_incidents where split_at is null
union all select 'omixor_cabinet_layouts', count(*) from omixor_cabinet_layouts where split_at is null;
```

log 裡搜尋 `[遷移警告]` 可看到略過的列與原因。

## 5. 救援 SQL

### 5.1 從新表重建 jsonb,並讓遷移重跑

用於:新版有問題、要在新版**之前**的資料狀態(舊版)上重做,或某幾列要重新從新表產生舊 jsonb 給舊程式用。每段都是「重建 jsonb → 把 `split_at` 設回 NULL」。**只能對你確定要重做的列做**:`split_at` 設回 NULL 之後,下次啟動遷移會以舊 jsonb 為準重新展開(覆蓋新表)。請先備份。

```sql
begin;

-- 頻道成員
update omixor_channels c set
  members = coalesce((select jsonb_agg(m.member order by m.pos) from omixor_channel_members m where m.org = c.org and m.channel_id = c.id), '[]'::jsonb),
  split_at = null;

-- 訊息提及
update omixor_channel_messages g set
  mentions = coalesce((select jsonb_agg(m.member order by m.pos) from omixor_channel_mentions m
                       where m.org = g.org and m.channel_id = g.channel_id and m.message_id = g.id), '[]'::jsonb),
  split_at = null;

-- 組織鏈的邊
update omixor_org_chains c set
  edges = coalesce((select jsonb_agg(jsonb_build_object('from', e.from_node, 'to', e.to_node, 'label', e.label) order by e.pos)
                    from omixor_org_chain_edges e where e.org = c.org), '[]'::jsonb),
  split_at = null;

-- 文件標籤
update omixor_docs d set
  tags = coalesce((select jsonb_agg(t.tag order by t.pos) from omixor_doc_tags t where t.org = d.org and t.doc_id = d.id), '[]'::jsonb),
  split_at = null;

-- 角色文件權限與例行任務
update omixor_agents a set
  doc_access = coalesce((select jsonb_object_agg(x.access_key, x.level) from omixor_agent_doc_access x where x.org = a.org and x.agent_id = a.id), '{}'::jsonb),
  routines = coalesce((select jsonb_agg(jsonb_build_object(
      'id', r.id, 'title', r.title, 'instructions', r.instructions, 'kind', r.kind, 'enabled', r.enabled,
      'everyHours', r.every_hours, 'hour', r.hour, 'minute', r.minute, 'weekday', r.weekday, 'priority', r.priority,
      'createdAt', r.created_at, 'lastRunAt', r.last_run_at) order by r.pos)
    from omixor_agent_routines r where r.org = a.org and r.agent_id = a.id), '[]'::jsonb),
  split_at = null;

-- 任務備註與連結
update omixor_agent_tasks t set
  links = coalesce((select jsonb_agg(l.url order by l.pos) from omixor_agent_task_links l where l.org = t.org and l.task_id = t.id), '[]'::jsonb),
  notes = coalesce((select jsonb_agg(jsonb_build_object('id', n.id, 'author', n.author, 'text', n.text, 'at', n.at) order by n.pos)
                    from omixor_agent_task_notes n where n.org = t.org and n.task_id = t.id), '[]'::jsonb),
  split_at = null;

-- 事件(data 整個重組;欄位名稱同 model.Incident 的 JSON)
update omixor_incidents i set
  data = jsonb_strip_nulls(jsonb_build_object(
    'id', i.id, 'title', i.title, 'sev', i.sev, 'status', i.status, 'commander', i.commander,
    'startedAt', i.started_at, 'resolvedAt', i.resolved_at, 'impact', i.impact, 'aiSummary', i.ai_summary, 'createdBy', i.created_by,
    'services', coalesce((select jsonb_agg(s.name order by s.pos) from omixor_incident_services s where s.org = i.org and s.incident_id = i.id), '[]'::jsonb),
    'followUps', coalesce((select jsonb_agg(f.ref order by f.pos) from omixor_incident_follow_ups f where f.org = i.org and f.incident_id = i.id), '[]'::jsonb),
    'alertIds', coalesce((select jsonb_agg(a.alert_id order by a.pos) from omixor_incident_alerts a where a.org = i.org and a.incident_id = i.id), '[]'::jsonb),
    'timeline', coalesce((select jsonb_agg(jsonb_build_object('id', e.id, 'date', e.at, 'kind', e.kind, 'text', e.text,
        'actor', jsonb_build_object('kind', e.actor_kind, 'id', e.actor_id)) order by e.pos)
      from omixor_incident_events e where e.org = i.org and e.incident_id = i.id), '[]'::jsonb))),
  split_at = null;

-- 文件櫃結構
update omixor_cabinet_layouts l set
  layout = coalesce((select jsonb_agg(jsonb_build_object('id', s.id, 'label', s.label, 'path', s.path,
      'tabs', coalesce((select jsonb_agg(jsonb_strip_nulls(jsonb_build_object('path', t.path, 'label', t.label, 'code', t.code, 'group', t.grp)) order by t.pos)
                        from omixor_cabinet_tabs t where t.org = s.org and t.project = s.project and t.section_id = s.id), '[]'::jsonb)) order by s.pos)
    from omixor_cabinet_sections s where s.org = l.org and s.project = l.project), '[]'::jsonb),
  split_at = null;

commit;
```

注意:事件與文件櫃重組的 JSON 欄位名稱(`startedAt`、`aiSummary`、`alertIds`、`followUps`、`code`、`group` 等)要和 `core/model` 裡的 json tag 一致;救援前先對照 `model.Incident`、`model.CabinetSection`、`model.AgentRoutine`,必要時調整。重建出來的 jsonb 只供遷移重新展開(或舊程式)使用。

### 5.2 只讓某幾列重新遷移(舊 jsonb 還在且正確時)

```sql
update omixor_docs set split_at = null where org = 'o' and id = 'DOC-1';   -- 其他表同理
```

重啟後遷移會以舊 jsonb 重新展開(覆蓋該列在新表的資料)。

## 6. 下個版本:drop 舊欄位

**前置條件(全部成立才能做):**

1. 線上所有執行個體都已經是這個版本(或更新),沒有舊版在寫資料。
2. `UnmigratedRows()` 為 0;或上面「升級後檢查」的 SQL 全部為 0。有略過的列要先修好舊資料、重啟、讓它展開。
3. 已經備份。
4. 同一個 commit 移除遷移與退回讀取的程式(`pg_split_migrate.go`、各 `expand*`、`migrate*`、`lenient*`、`guardUnsplit` 與各讀取裡的 `hasUnsplit()` 分支)、`pg.go` 的 `split_at`、舊欄位的建表語句。

```sql
begin;
alter table omixor_channels drop column members, drop column split_at;
alter table omixor_channel_messages drop column mentions, drop column split_at;
alter table omixor_org_chains drop column edges, drop column split_at;
alter table omixor_docs drop column tags, drop column split_at;
alter table omixor_agents drop column doc_access, drop column routines, drop column split_at;
alter table omixor_agent_tasks drop column notes, drop column links, drop column split_at;
alter table omixor_incidents drop column data, drop column split_at;
alter table omixor_cabinet_layouts drop column layout, drop column split_at;
commit;
```

drop 欄位會一併刪掉 `<表>_unsplit` 部分索引。drop 之後同樣**不可回滾**(只能從備份還原)。

## 7. 身分登記層不部署

曾規劃的資料庫身分登記層(`omixor_principals` 登記表 + 19 個 `<欄位>_ref` 產生欄位 + 19 條 `fk_p_*` 外鍵 + 3 個 SQL 規範化函式)**不部署**;ISS-36 直接進 actor 欄位(見雲端 DOC-11)。

- Go 的規範化規則(`core/model/principal.go`:`PrincipalNode`、`PrincipalKind`、`AssigneeNode`、`ActorNode`)保留,是 actor 遷移的對照來源。
- 資料庫裡所有「誰」的欄位(表、欄位、寫法)的清單在 `core/store/identity_fields.go`(`IdentityFields`),測試確認每個欄位都存在。
- 正式庫沒有部署過這一層,不需要清理。開發機的資料庫若跑過那一版,會殘留登記表、`_ref` 欄位與 `fk_p_*` 外鍵,程式不會自動刪;不影響運作(`_ref` 是產生欄位,不影響寫入),要清乾淨就手動 drop(先 drop 外鍵與欄位,再 drop 函式與表),或重建資料庫。

## 8. 大表與維護窗口腳本

- STORED 產生欄位 `project_ref`(`omixor_docs`、`omixor_cabinet_sections`、`ops_issues`)新增時整張表重寫並持有 ACCESS EXCLUSIVE 鎖。啟動時每次重寫前先查 `pg_total_relation_size`(含索引與 TOAST):超過 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB`(預設 1024,0 = 不限制)就**不自動做**,只警告、記入啟動彙總。
- 這些表請在維護窗口先跑 `sql/premigrate.sql`(舊名 `principals_pre.sql` 已改名;現在只含 `project_ref` 與前置的 `project` 欄位補齊)。用與服務相同的資料庫角色(表的擁有者);`psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f premigrate.sql`,**不可加 `-1`**(單一交易會把每張表的鎖持有到整份結束)。腳本每句各自 autocommit,每句前 `set lock_timeout = '5s'`(session 層級),最後 `reset lock_timeout;`;中途失敗時 psql 結束、連線關閉,設定不殘留;重跑整份即可(`add column if not exists`)。舊結構(還沒有 `ops_issues.project`)腳本會先冪等補 `project text not null default ''`(常數預設值,不重寫)。

## 9. ISS-36 階段 B-2a:`omixor_actors`

actor(使用者、角色、系統內建署名的共同身分)的表。規格見雲端 DOC-11、DOC-13。**這一批只新增這張表與固定的系統 actor**:`omixor_users`、`omixor_agents`、`omixor_org_members`、`omixor_tokens` 與所有「誰」的欄位都沒改,也沒有任何外鍵指到它(只有 `owner` 自我參照);後續工作包(B-2b 使用者、B-2c 角色、C-1 欄位遷移)才接上。實作在 `pg_actor.go`,型別與規則在 `core/model/actor.go`。

### 表結構(`pgActorSchema`,由 `migrateActors` 建立,不放在 `pgSchema`)

```sql
create table if not exists omixor_actors (
  id          text primary key,
  kind        text not null check (kind in ('user','agent','system')),
  org         text,                      -- 只有 agent 有;不掛 omixor_orgs 外鍵(刪組織時 actor 只退役、不刪列)
  name        text not null,
  handle      text not null check (btrim(handle) <> ''),
  avatar      text not null default '',
  owner       text references omixor_actors(id) on delete restrict,   -- agent 的負責人(使用者 actor),可為 NULL
  disabled_at timestamptz,               -- 退役、停用時間
  created_at  timestamptz not null default now(),
  unique (id, kind),
  check ((kind = 'user'   and id like 'usr\_%' and org is null)
      or (kind = 'agent'  and id like 'agt\_%' and org is not null)
      or (kind = 'system' and id like 'sys\_%' and org is null)),
  check (kind <> 'user' or handle = lower(handle)),
  check (owner is null or kind = 'agent')
);
create unique index omixor_actors_user_handle   on omixor_actors (lower(handle)) where kind = 'user';
create unique index omixor_actors_system_handle on omixor_actors (handle)        where kind = 'system';
create unique index omixor_actors_agent_handle  on omixor_actors (org, handle)   where kind = 'agent' and disabled_at is null;
create index        omixor_actors_agent_org     on omixor_actors (org)           where kind = 'agent';
```

與 DOC-13 §1.1 的差異:多三條只擋不可能資料的 check(handle 不能是空白、使用者 handle 一律小寫、owner 只有角色可以有)。`omixor_actor_aliases`(DOC-13 B-2a 順帶列的對照表)不在這一批,放 C-1(它只服務舊值對照)。

id 格式:`usr_<uuidv7>`、`agt_<uuidv7>`、`sys_<名稱>`;check 只驗前綴與 kind 一致,UUID 格式由 `model.ActorKindOfID` 在寫入前驗。唯一性:使用者 handle(email)全伺服器唯一、不分大小寫,停用的使用者仍佔名字;角色 `(org, handle)` 只在現役時唯一,退役後名字讓出來,同名再建是新的 actor;系統名稱唯一。

### 系統 actor(固定資料列)

| id | 名稱 | handle | 取代的舊署名 |
|---|---|---|---|
| `sys_omixor` | omixor 系統 | `omixor` | `SystemAuthor`、舊 `system`、事件的 system 種類 |
| `sys_routine` | 例行排程 | `routine` | `RoutineAuthor` |
| `sys_chain` | 組織鏈 | `chain` | `ChainAuthor` |

清單在 `model.SystemActors`,id 常數 `model.SystemActorOmixor/Routine/Chain`:**id 一經發佈就不能改**。系統 actor 的名稱、handle、停用狀態由程式固定:`Store` 的改名、改 handle、停用都回 `ErrSystemActor`。遷移才建的 `sys_legacy_*`(對不上的舊值)不在這一批,由 C-1 建立。

### 冪等、並行與順序

- `migrateActors` 在 advisory lock(`omixor-actors-migrate`,`xactLock`)內的單一交易裡建表、建索引、寫入系統 actor;多個實例同時啟動時排隊,後到的看到表已存在、列已是目前的值,什麼都不寫(系統 actor 用 `on conflict (id) do update … where <有差異>`,值相同不產生新的列版本;被手動改壞的會在下次啟動校正回來)。等鎖超過 `migrateLockWait` 只警告略過、下次啟動再做。
- 順序:`startupMigrations` 裡在 `migrateSplitIncidentsTasks` 之後、`migrateForeignKeys` 之前;`migrateForeignKeys → migrateIssueKeys → migrateSeq` 的順序不變(`TestActorMigrationOrder` 把關)。步驟是 `soft`:只有整體預算用完才降為警告,否則結構性錯誤讓啟動失敗。
- 不碰任何既有表,不重寫、不鎖既有的大表;因此沒有大小門檻問題(表本身只有使用者數 + 角色數 + 3 列)。
- 已知(既有行為,與這一批無關):`pgSchema` 的 `create table if not exists` 本身不防並行,兩個實例同時對**全新**資料庫建表可能撞 `pg_type` 唯一索引;`omixor_actors` 不在 `pgSchema` 裡,所以不受影響。
- 回滾:新表,舊版不認得也不使用,**可以直接留著**(舊版啟動不會動它);要移除用 `drop table omixor_actors cascade`(之後有外鍵指到它的版本則不行,要從備份還原)。

### 測試資料庫

`omixor_actors` 登記在 `core/storetest/testdb` 的結構中繼資料表(`structureMetaTables`)並附固定列條件(`metaTableKeep`:`kind = 'system'`):範本裡只有三筆系統 actor;回收時其他列(測試建的使用者、角色)被刪掉、系統 actor 保留;結構指紋只算固定列,所以測試建 actor 不會害資料庫被丟棄(只有改了系統 actor 才會)。

## 10. ISS-34:Issue key 遷移(`migrateIssueKeys`)

做什麼、順序與一次性行為見 `docs/release-db-2026-11.md` 1.4。這裡記實作要點與實測。

- **集合式**:`assignIssueKeys` 在記憶體算每一筆的新 id、key、號碼(保留原 `ISS-<n>`,key 被別名占用就改發新號;新號碼從序號表下一號起算,用完 `seqBump` 回記);`applyIssueKeys` 把結果以 `COPY` 放進暫存表 `_issue_key_map`(再建 `_issue_key_ci`,同一個 `upper(舊 id)` 取字典序第一筆),然後 `ops_issues`、`omixor_channels`、`omixor_agent_task_links`、`omixor_issue_keys` 各一句 `update … from` / `insert … select`。留言與有外鍵的頻道靠 `on update cascade`。
- **交易與逾時**:整段在一個交易、一把 advisory lock(`omixor-issue-keys-migrate`)。逾時(預算 10 分鐘與啟動剩餘預算取短)或失敗:**整段回滾**,下次啟動重跑(只處理 `key = ''` 的列)。冪等:重跑不改已做過的。
- **孤兒**:做完輸出頻道、任務連結(UUID 或 `<字母數字>-<號碼>` 形狀)找不到 Issue 的筆數;只統計。
- **孤兒參照的 key 不再發**(`reserveOrphanIssueKeys`,遷移演練 95c2be7 → HEAD 發現):舊版永久刪除 Issue、用 max+1 發號,序號表從 `max(number)` 起算,被刪掉的最大號(頻道 `issue_id`、任務連結還寫著 `ISS-11`)遷移後會再發一次。啟動步驟 `migrateSeq` 之後**每次啟動**跑一句 `insert … select … on conflict do nothing`:頻道 `issue_id` 與任務連結中形狀 `^[A-Za-z][A-Za-z0-9]{1,9}-[0-9]+$`、別名表沒有這個 key、也不是任何 Issue id 的,正規化(大寫、號碼去前導零)後記進 `omixor_issue_keys_retired`(reason `orphan-ref`;`model.ReservedProjectKeys` 除 ISS 外的前綴不算)。`nextIssueKey` 與 key 遷移的重新發號都跳過這張表的 key。選這個而不是 `seqBump`:只跳過被參照的號碼(打錯的 `ISS-99999` 不會讓序號暴增),而且以 key 文字比對,專案代號形狀的孤兒(`ABC-2`)在之後任何用這個代號的專案都會跳過。交易層級 advisory lock `omixor-issue-keys-retire`,等不到或逾時只警告;成本是頻道、任務連結各一次掃描,沒有新孤兒時不寫入。不改、不刪任何使用者資料。
- **新版本身的同類情況**:同範圍的序號不回收,所以單一範圍內不會重發;但專案刪除後別的(或新的)專案用同一個代號時序號重新起算,永久刪除的 Issue 留下的任務連結就會撞上。`DeleteIssue` 因此在同一個交易裡把該 Issue 的所有 key 記進 `omixor_issue_keys_retired`(reason `deleted`)。垃圾桶(`TrashIssue`)保留 key 與別名,不受影響。測試:`pg_issue_keys_retired_test.go`。
- **舊索引**:`ops_issues_org_number` 的換新在 `ensureSeqIndex`(`pg_seq.go`),不依賴本遷移。比對欄位 `(org, project, number)`、唯一、部分索引(`number > 0`)、有效;不符就在 `_new` 名稱下 `create unique index concurrently`,成功才 `drop` 舊的並 `rename`。現有資料在新欄位組合上有重複時只警告、保留舊索引。
- **發號避開別名**:`CreateIssue`、`MoveIssue`、匯入的自動發號都走 `nextIssueKey`:號碼若被別名表(搬走的舊 key、舊格式 id)或現有 key 占用就跳號;`addIssueKey` 的「目前 key」寫入不再覆蓋指向別張 Issue 的別名(回 `ErrIssueKeyTaken`)。建專案、改代號時 service 的 `checkProjectKey` 另外擋掉別名表已有 `<代號>-<n>` 過期別名的代號(400)。

### 實測(2026-11-20,本機 docker PG 17,測試庫)

資料:舊結構(以 HEAD 的 `NewPG` 建好舊 schema,再灌資料)2 萬 Issue(其中 2 千筆是 `gh-<n>` 非數字 id)、10 萬留言、2 萬任務(各 1 條連結指向 Issue)、2 千頻道(各綁一張 Issue)。

| 版本 | Issue key 這一步 | `NewPG` 整體(含其他遷移) |
|---|---|---|
| 修正前(逐筆 update,每筆全表掃任務連結) | 約 238 秒 | 約 308 秒(審查另測約 7 分鐘) |
| 修正後(集合式) | 約 5.2 秒 | 約 71 秒 |

預算 10 分鐘。修正後驗證:`key` 全數非空、任務連結與頻道與留言全部換成新 id、別名 2.2 萬筆(2 萬 key + 2 千舊 id)、序號表 `last` 對、`ops_issues_org_number` 已換成 `(org, project, number)`、孤兒 0。

重現方法(不要留在工作樹):暫存目錄建兩個獨立 go module,一個 `replace omixor-core` 指到 `git worktree` 出來的 HEAD(建舊 schema),一個指到工作樹(跑新版 `NewPG`),中間用 `generate_series` 灌資料。

## 11. ISS-36 階段 B-2b:使用者接上 actor(`migrateActorUsers`)

規格:雲端 DOC-13 §1.2、§2、§8(D1、D3、D11)。實作在 `pg_actor_users.go`,`SaveUser` 在 `pg_user.go`。

**這一版只「轉換」(D11)**:`omixor_users.id` 維持 `u_<x>`(`omixor_tokens`、`omixor_org_members`、`omixor_audit` 的 `user_id` 都不動);`email`、`name` 欄位也還在(actor 的 `handle` = email 小寫、`name` 是複本,`SaveUser` 每次同步)。id 改寫成 `usr_…`、移除 `email/name`、`actor_id` 改 NOT NULL 都留給下一版(舊 revision 滾動部署期間仍會寫沒有 `actor_id` 的使用者列,所以欄位是可為 NULL 的)。

### 結構

```sql
alter table omixor_users add column actor_id text;                                     -- 指到 omixor_actors(id),可為 NULL
alter table omixor_users add column kind text not null default 'user' check (kind = 'user');
create unique index omixor_users_actor_id on omixor_users (actor_id);
alter table omixor_users add constraint fk_users_actor foreign key (actor_id, kind)    -- 擋住指到角色、系統 actor
  references omixor_actors (id, kind) on delete restrict not valid;                    -- 補齊後 validate
```

### 步驟與行為(順序:`migrateActors` → `migrateActorUsers` → `migrateForeignKeys` → `migrateIssueKeys` → `migrateSeq`)

1. session 層級 advisory lock `omixor-actor-users-migrate`;**等不到鎖視為失敗(啟動失敗)**:使用者登入要這一步的欄位,不能略過。
2. 欄位、索引、外鍵都先查在不在,在就完全不下 DDL;加欄位用 `lock_timeout 3s` 與重試(只改目錄,不重寫表)。
3. 補齊:每批 2000 位 `actor_id is null` 的使用者(`for update skip locked`),整批用集合運算。每位的 handle = email 規範化(小寫、去空白);
   - 已有同 handle、沒被別的使用者綁住的使用者 actor(「未註冊使用者」,D3)→ **接回**,不建第二個,名稱換成使用者的;
   - 否則建 `usr_<uuidv7>` actor(uuid 的時間用使用者的 `created_at`);
   - email 不能當 handle(空的、含空白)或 handle 與別人重複(`omixor_users.email` 的唯一區分大小寫,`A@x`、`a@x` 可以同時存在)→ 後面的改用使用者 id 當 handle,寫 `[遷移警告]`;
   - 預算(`migrateStartupBudget`)用完就停,不加外鍵,下次啟動接著做;補齊後 `not valid` 加外鍵再 `validate`。
4. 冪等:全部補完後重跑只查幾個目錄、什麼都不寫(測試以 `xmin` 驗證列沒被重寫)。多實例同時啟動排隊,後到的看到已完成。
5. 大小:只有列鎖,不重寫表;沒有 `maxAutoRewriteBytes` 問題(使用者表小)。耗時(本機 Docker PG,2 萬位使用者):補齊 + 加外鍵 + validate 約 1.4 秒;從 HEAD 結構的資料庫(2 萬位)整個 `NewPG` 約 2.5 秒;已完成後重啟約 0.2 秒。

### `SaveUser` 之後的行為

`SaveUser` 在同一個交易內取得或建立 actor:先用 `u.ActorID`(要是存在的使用者 actor,否則 `ErrBadRef`)、再用已存的 `actor_id`、再依 email 小寫接回既有 actor、最後才建新的;同時有人用同一個 email 建立(多實例同時登入)只會有一個。停用(`Disabled`)不影響 actor,使用者的登入開關仍是 `omixor_users.disabled`;改 email 同步 actor 的 handle(新 handle 已被別的 actor 佔著時維持原 handle 並寫警告)。

### 回滾

不支援(同 §1)。新欄位舊版不認得也不使用,可以留著;要移除:`alter table omixor_users drop constraint fk_users_actor; drop column actor_id, drop column kind`(舊版的 `omixor_users` 沒有這兩欄,drop 後就是舊結構,actor 列留著無害)。


## 12. ISS-36 階段 B-2c:角色接上 actor(`migrateAgentActors`)

規格:雲端 omixor DOC-13 §1.2「案 B」。程式:`pg_actor_agents.go`、`pg_agent.go`(`SaveAgent`、`DeleteAgent`、`ImportAgents`)、`pg_org.go`(`DeleteOrg`)、`pg_actor.go`(`SetActorOwner`、`unique (id, org, handle)`)。

### 結構

- `omixor_agents.actor_id text`:可為 NULL(見下)、唯一(`omixor_agents_actor_id_key`)。
- 複合外鍵 `omixor_agents_actor_fk`:`(actor_id, org, id) → omixor_actors (id, org, handle)`(先 `not valid` 再 `validate`;`actor_id` 為 NULL 的列不檢查)。actor 的 org、handle 一定等於角色的 org、id;沒有 `on update cascade`:角色 id 不能改(`SaveAgent` 沒有改名功能,不同 id = 新角色),`UpdateActorHandle` 對角色 actor 會因為被角色參照而失敗。要做角色改名,要同時改這個外鍵、`omixor_agent_tasks` 的外鍵(加 `on update cascade`)與以角色 id 字串存的欄位(任務、憑證、組織鏈 `agent:<id>` 節點、Issue assignee、頻道成員),另案。
- `omixor_actors`:`unique index (id, org, handle)`(給上面的外鍵指)。
- `actor_id` **不加 NOT NULL**:第一次正式部署是滾動更新,舊 revision 寫入的角色沒有 `actor_id`;每次啟動只查一次有沒有 NULL 並補上(D11:第一次部署只轉換,不加會擋舊版寫入的約束)。

### 同步規則(同一個交易,失敗全部回滾)

| 操作 | actor |
|---|---|
| `SaveAgent` / `SaveAgentOwned` 新增 | 建 `agt_` actor(org、handle = 角色 id、name、建立時間 = 角色的;`active = false` 直接是停用);owner = 指定的人,沒指定 = 組織第一位管理員成員 → 最早的啟用中伺服器管理員(只認有使用者 actor 的人),找不到 NULL |
| `SaveAgent` 修改 | `name`、`disabled_at` 跟著(停用/啟用);owner、handle 不動 |
| `DeleteAgent` | actor 退役(`disabled_at`,列保留);同 id 重建 = 新的 actor |
| `DeleteOrg` | 該組織全部角色 actor 退役 |
| `ImportAgents` | 每個角色建 actor |

角色 id 不符 handle 規則(`^[a-z0-9][a-z0-9-]{0,63}$`)的舊資料沒有 actor(`actor_id` NULL,功能照舊,遷移每次啟動寫一行警告)。

### 啟動遷移(`migrateAgentActors`,在 `migrateActorUsers` 之後、`migrateForeignKeys` 之前)

session advisory lock(等不到就略過這一輪)→ 表大小門檻(`OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB`)→ 加欄位(`fkStep`:`lock_timeout` + 重試)→ 為沒有 `actor_id` 的角色建 actor(每批 200 個一個交易、預算用完就停)→ 對齊(名稱、停用狀態以角色列為準;沒有角色列指著的現役角色 actor 退役,例如舊 revision 刪角色的殘留)→ 唯一約束、複合外鍵(`not valid` → `validate`,失敗只警告)→ 補 owner(`BackfillAgentOwners`)。冪等:重跑不改任何列;已就位時成本是幾個 catalog 查詢與一次 `actor_id is null` 查詢。

### 回滾

不支援(同 §1)。新欄位舊版不認得也不使用,可以留著;要移除:`alter table omixor_agents drop constraint omixor_agents_actor_fk, drop constraint omixor_agents_actor_id_key, drop column actor_id`(actor 列留著無害)。

## 13. ISS-36 階段 C-1:「誰」欄位遷移引擎(`migrateActorColumns`)

規格:雲端 omixor DOC-13 §1.4(逐欄表)、§1.5(寬鬆解析器)、§2.1、§2.5。程式:`pg_actor_columns.go`(引擎)、`pg_actor_legacy.go`(寬鬆解析器、`omixor_actor_aliases`)、`identity_fields.go`(`ActorColumns`:22 欄)、`pg_fk.go`(`fkSpecs()` 登記 actor 外鍵)、`pg_migrate_guard.go`(啟動步驟與彙總)。測試:`pg_actor_columns_test.go`、`pg_actor_legacy_test.go`、`pg_migrate_harden_test.go`(`TestActorColumnsGuard`)。

### 觸發方式(C-2 共用地基之後:沒有開關)

C-1 原本用兩個環境變數(`OMIXOR_MIGRATE_ACTOR_COLUMNS`、`OMIXOR_MIGRATE_ACTOR_FK`,預設關)把引擎擋在 C-2 之前。C-2 共用地基之後這兩個變數**移除**,理由與取捨:

- 程式碼(store、服務層)只認「最終結構」:22 個「誰」欄位是 actor id、外鍵指向 `omixor_actors`、組織鏈欄位叫 `from_actor`/`to_actor`、事件只有一個 `actor`、稽核是 `actor`。所以轉換不再是「危險的選項」,而是**程式能正常運作的前提**;留一個「可以不轉」的開關等於留一個會讓服務讀寫錯位的設定。
- **全新資料庫**(以及 storetest 範本庫):建表語句(`pg.go:pgSchema`、`pg_split_incidents_tasks.go`)直接就是最終結構;同一次啟動最後一步,這個引擎對空表補完成標記與外鍵(瞬間)。**舊結構的資料庫**:引擎原地轉換成同一個結構。兩條路殊途同歸,有結構指紋測試證明(`pg_actor_final_test.go:TestActorLegacyStructureConvertsToFinal`:舊結構、有各種舊值的資料庫跑完引擎 == 全新建立的資料庫)。
- **沒轉完就不能啟動**(`migrateActorColumnsStep` 回錯誤,不是 `soft`):預算用完、等不到鎖、表超過 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB` 的自動遷移門檻、驗證失敗,啟動都會失敗並列出欄位與原因;已轉換的進度每批一個交易保留,**重啟會接著做**。取捨:啟動失敗比「半轉換卻照常服務」安全(後者讀到空的「誰」、寫入被外鍵擋);代價是大表要調大門檻(或設 0)並重啟,Cloud Run 要有足夠長的 startup probe。**沒有例外**:還有拆表遷移留下、展開不了的舊 jsonb 列(`UnmigratedRows() != 0`,例如組織鏈 `edges` 是字串、頻道 `members` 是物件)時,引擎整個沒做(22 欄一個都沒轉),**啟動同樣失敗**——以前這種情況只警告、照常服務,結果 Issue 的 `created_by` 仍是舊字串、該組織鏈寫入被拒、其他組織鏈寫入走過渡橋把 actor id 寫進舊結構且沒有外鍵,正是上面說的「半轉換卻照常服務」。失敗訊息列出哪張表、哪個主鍵(組織/頻道…)、哪個舊 jsonb 欄位壞了,以及怎麼修(見下面「啟動失敗的訊息與處理」)。
- 不考慮滾動部署時的舊版本(使用者決定):組織鏈欄位改名不做相容 view;轉換完成後立刻加外鍵(`not valid`,背景 `validate`),舊 revision 寫舊字串會被外鍵擋下。
- 轉換完成的複製欄位,舊來源欄位(事件的 `actor_kind`、`actor_id`,稽核的 `user_id`)驗證通過後**立即 drop**(只改 catalog),不再等下個版本,指紋才會一致。稽核的 `email` 快照、`omixor_users.email/name` 保留。
- 寫入橋(`pg_actor_bridge.go`):一般路徑已不經過;只剩舊 jsonb 展開遷移與 incident/channel 匯入寫入時,把舊字串經寬鬆解析器轉成 actor id。讀取的 SQL 版(`whoSQL` 等)已刪除。細節見 `ACTOR_CONTRACT.md`。

### 22 個欄位(`ActorColumns`)

`IdentityFields` 的 19 欄 + `ops_issues.deleted_by`、`omixor_server_features.updated_by`、`omixor_audit.user_id`。三種方式:

| 方式 | 欄位 | 說明 |
|---|---|---|
| 原地換成 actor id | 其餘 17 欄 | 欄位名不變;原本可為空的欄位(`''` = 沒有)轉換後 `drop not null`、`drop default`,空白值改 NULL;原本 NOT NULL 的維持 NOT NULL(空字串 → `sys_legacy_unknown`) |
| 換完後欄位改名 | `omixor_org_chain_edges.from_node` → `from_actor`、`to_node` → `to_actor` | 瞬間操作(`lock_timeout` + 重試) |
| 複製到新欄位 | `omixor_incident_events`(`actor_kind` + `actor_id` → `actor`)、`omixor_audit`(`user_id` → `actor`) | 結果寫進新的 nullable 欄位,驗證通過後 **drop 舊來源欄位**(`actor_kind`、`actor_id`、`user_id`;稽核的 `email` 快照與 `omixor_users.email/name` 保留);事件的 `system` → `sys_omixor`,稽核的 `u_<x>` 對 `omixor_users.actor_id`、`agent:<id>` 依該列的 `org` 對角色 |

稽核另有 `source`(DOC-13 §1.4 #22 的 `via`,C-2e:login、pair、mcp、local、agent-credential,可接任意字串;`text not null default ''`,和 detail 裡的 `via` 登入方式不同)欄位:全新資料庫在 `pgSchema` 建表時就有,舊資料庫由 `bootstrapSchema`(`pg_schema_boot.go`)補。`bootstrapSchema` 是每次啟動的建表流程:`pgSchema` 只留 `create table if not exists`(表已存在時不碰表鎖),欄位(`pgSchemaColumns`,含稽核的 `source`)與索引(`pgSchemaIndexes`)先查 catalog(`information_schema`、`to_regclass`,不拿目標表的鎖)缺才補,補的時候走 `fkStep`(`lock_timeout` + 重試)。以前 `pgSchema` 每次啟動執行 22 個 `alter table … add column if not exists` 與 14 個 `create index if not exists`,即使已存在也要拿表鎖(前者 ACCESS EXCLUSIVE、後者 SHARE),等鎖時會把熱表的所有讀寫排在後面,而且整段是一個隱含交易、15 秒 context、沒有 lock_timeout 與重試。結構指紋與原本一致(`TestBootstrapSchemaNotBlockedByTableLock`:另一個連線持有熱表 ACCESS EXCLUSIVE 鎖時,結構完整的資料庫建表不被擋;`TestBootstrapSchemaAddsMissingColumnsAndIndexes`:缺的補得回來)。全新資料庫的最終結構裡,上表「方式」只剩結果:`from_actor`/`to_actor`、事件的 `actor`、稽核的 `actor`。

### 對照規則(寬鬆解析器 `ResolveLegacy`)

依 DOC-13 §1.5:空值 → NULL(NOT NULL 欄位 → `sys_legacy_unknown`);已經是存在的 actor id → 自己;`agent:<id>` 與 `assignee` 的純角色 id → 該組織的角色 actor,角色已刪 → 同 handle 最新的退役 actor,都沒有 → `sys_legacy_<雜湊>`;`user:<x>`、含 `@` → 找使用者,沒有 → 建「未註冊使用者」actor;`routine`/`chain`/`omixor`/`system` → `sys_routine`/`sys_chain`/`sys_omixor`;`mcp` 與本機別名(`omixor-console`、`ops-web`、`SetMigrationLocalUser` 設定的別名、`local:<x>`)→ 本機使用者(沒設 → `sys_legacy_mcp` / `sys_legacy_<雜湊>`);其他 → `sys_legacy_<sha256(規範化節點) 前 8 碼>`(名稱 = 原值),寫 `omixor_actor_aliases` 與 log。同一個舊值永遠同一個 actor(冪等)。角色依組織解析,所以不查別名表。

審查後補的細節:

- **超長舊值**:`omixor_actor_aliases.alias` 是 btree 主鍵(單列約 2.7 KB 上限),原值超過 512 位元組的節點改存 `sha256:<全雜湊>`(穩定、冪等、可重跑);actor 名稱仍存完整原值。log 與錯誤訊息裡的舊值只印前 80 字元加總長度。
- **「空白」的定義**:SQL 端(`blankRe`)與 Go 的 `strings.TrimSpace` 一致(含 NBSP U+00A0、全形空白等 Unicode 空白),不用 PostgreSQL 的 `\s`(依 locale,NBSP 不一定算)。已實測 `\s` 不一致會讓可為 NULL 的欄位驗證永遠失敗,現在空白值一律 → NULL(NOT NULL 欄位 → `sys_legacy_unknown`)。
- **雜湊碰撞**:8 碼雜湊已被「另一個原值」的別名佔用時,改用 16、32、64 碼,寫 `[遷移警告]`;不會靜默併成同一個 actor。別名表記得誰佔了哪個 id,所以重跑穩定。
- **不同組織同名的已刪角色**(`agent:foo`):節點帶組織(`org/<org>/agent:foo`)→ 各自一個 `sys_legacy` actor,不併(語意上是不同的人)。沒有組織的欄位(`omixor_server_features.updated_by`)仍是「同一原值同一 actor」。取捨:`sys_legacy` 數量略增(每個組織每個已刪角色一個),換來權限、歸屬不會跨組織混在一起;C-1 尚未部署,沒有既有資料要相容。
- **前綴大小寫不分**:`Agent:dev`、`USER:a@x`、`Local:foo` 先正規化成小寫前綴再解析。
- **同 email 只差大小寫的多帳號**:`UserActor` 選最早建立的(`created_at`、`id`)並寫 `[遷移警告]`,不再 `limit 1` 任選。本機使用者要在 `NewPG` 之前用 `store.SetMigrationLocalUser(handle, 別名…)` 告訴遷移(console、omixor-local 的接線屬 C-5)。

與 DOC-13 表格的小差異:不含 `@` 的純名稱(`tim`)若有同 handle 的使用者就對到他;`user:<x>`(x 不含 `@`)不是別名時,**先查 `omixor_users.id = x`(使用者 id 寫法,例如 `user:u_a`;id 大小寫保留),再查 handle = x**,都找不到才建未註冊使用者(以前直接建 handle = `u_a` 的未註冊使用者,對不到真正的 `u_a`);內含 `< > , ; "` 的值(組織鏈測試用的串接字串)不當 email,走 sys_legacy。

### 順序

`migrateActors → migrateActorUsers → migrateAgentActors → migrateActorAliases → migrateForeignKeys → migrateIssueKeys → migrateSeq → migrateActorColumnsStep`。放最後是為了不和 Issue key、序號兩個大動作爭 `ops_issues`;外鍵由引擎自己在欄位驗證通過後加,趕在 `finishStartup` 開背景 validate 之前。`migrateForeignKeys` 不加、背景補外鍵也不加 actor 外鍵(只 validate 已存在的)。

每個欄位的步驟(`convertActorColumn`):

1. 步驟 0(整個引擎一次,`pg_actor_columns_precheck.go`):`omixor_actors`、`omixor_actor_aliases` 存在;`omixor_agents.actor_id`、`omixor_users.actor_id` 存在且對應的前一步沒有被略過(角色、使用者 actor 遷移因等不到鎖、表太大、預算用完被略過時,明確說是哪一步、為什麼、怎麼處理,不讓 17 欄各自噴 `column g.actor_id does not exist`);`UnmigratedRows() == 0`;偵測開發機殘留的身分登記層(`omixor_principals`,只警告)。前三項任何一項不過 = 引擎整個略過,`migrateActorColumnsStep` 在還有欄位沒轉時回錯誤、啟動失敗。
2. 已有完成標記(欄位註解 `omixor:actor-converted`)而且外鍵已在 → 跳過,不掃表。沒有舊來源欄位的複製欄位(全新資料庫、或舊欄位已 drop)只驗證目標欄位後寫標記、加外鍵(`finishActorColumn`)。
3. 表大小超過 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB` → 不自動轉(這一欄略過,最後啟動失敗並列出,要在維護窗口處理或調大門檻)。**例外:全空的欄位**(沒有任何非 NULL 值,例如舊庫剛加的 `ops_issues.deleted_by`;`exists` 一次唯讀掃描,找到有值的一列就停)沒有東西要轉、不產生舊 tuple 與 WAL,不受門檻限制。只算全 NULL;有 `''` 的欄位要把 `''` 改成 NULL 或 `sys_legacy_unknown`,仍受門檻限制。複製欄位(事件、稽核)不適用。
4. 去重的舊值 → 解析 → 建缺的 actor、記別名。
5. 主鍵含 member 的表(`omixor_channel_members`、`_mentions`、`_reads`)先刪「轉換後會撞主鍵」的重複列:成員、提及留 `pos` 小的,已讀留 `last_read` 最大的(同一人多種寫法的已讀位置不倒退;`last_read` 相同再留 `at` 新的)。
6. 批次原地更新(`pg_actor_columns_batch.go`:以 `ctid` 往前推進取批,每批 `ctid > <上一批的邊界> … limit N`,預設每批 5000 列、每批一個交易、批間檢查啟動預算,用完就停、下次續做;只有列鎖,不重寫整張表)。不重掃已轉換的前綴:舊寫法每批 `limit N` 無排序、從表頭掃起,總成本是列數的平方(60 萬列約 5 秒、500 萬列約 6 分鐘);現在是線性(`TestActorBatchUpdateScalesLinearly` 比 N 與 2N 的耗時比,約 2.0;舊寫法的規劃器行為下約 3.1 以上)。每批交易內 `set local enable_seqscan = off`、`max_parallel_workers_per_gather = 0` 讓規劃器走 TID Range Scan(PostgreSQL 14 起;它不保證排序輸出,查詢不寫 `order by`,否則規劃器會改成整表平行掃描加排序,每批都掃全表)。實體順序是實作行為不是保證,所以掃到表尾之後會再確認沒有符合條件的列(`exists`),有剩就從頭再掃一輪(最多 3 輪);最後仍由步驟 7 的驗證兜底。update 的條件除了 `ctid` 還要「舊值欄位 = 取到的舊值」:被鎖住的列等對方 commit 後,PostgreSQL 對最新版本重新檢查條件,而 TID 掃描的重新檢查不比對 ctid,只比 ctid 會把對方剛寫的新值蓋掉。
7. 驗證:轉換後指向不存在的 actor 的列數必須 0;非 0 → 這一欄略過後面的步驟、寫 `[遷移警告]`(含前 10 個值),不加外鍵,最後啟動失敗並列出(進度保留)。批次更新時被並行修改而跳過的列(舊程式仍在寫舊字串)訊息是「有 N 列在轉換期間被並行修改,將於下次啟動重試」,不是「孤兒」;兩者並存時分開列。
8. 結構:`drop not null`、空白 → NULL、欄位改名;最終驗證(嚴格)→ 複製欄位 drop 舊來源欄位 → 寫完成標記 → 加外鍵(`not valid`,背景 `validate`)。

完成標記與外鍵:標記用欄位註解(catalog 操作,每次啟動不掃大表)。有標記、外鍵還沒加(上一輪在標記之後、加外鍵之前中斷,或外鍵被拿掉)時,加之前再驗證一次,發現轉換之後才寫入的舊字串就作廢標記、重新轉換,再加外鍵。

### 啟動失敗的訊息與處理

`migrateActorColumnsStep` 在還有欄位沒轉完時回錯誤(列出欄位與原因),已轉換的進度保留,修好後重啟會接著做:

| 訊息裡的原因 | 怎麼處理 |
|---|---|
| `還有 N 列沒有展開(UnmigratedRows != 0)` + 表、主鍵、舊 jsonb 欄位 | 該列的舊 jsonb 壞了(不是合格的 JSON 結構):修復那一欄,或刪除該列(訊息附 `delete from …` 範例),重啟。若訊息同時列出「拆表遷移 … 逾時或啟動預算用完」,代表只是拆表沒做完:調大 `OMIXOR_MIGRATE_BUDGET_MINUTES` 或直接重啟,已展開的進度保留 |
| `角色 actor 遷移`/`使用者 actor 遷移` 被略過 + `omixor_agents.actor_id`/`omixor_users.actor_id` | 原因寫在括號:等不到遷移鎖(另一個實例在做,等它結束後重啟)、表超過門檻(調大 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB` 或設 0)、預算用完(調大 `OMIXOR_MIGRATE_BUDGET_MINUTES` 或重啟) |
| `<表.欄位>(表 … 超過自動遷移的門檻)` | 調大 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB` 或設 0(全空欄位不受此限) |
| `<表.欄位>(驗證失敗:N 筆孤兒)` | 看 `[遷移警告]` 的前 10 個值,對不上 actor 的舊值 |
| `<表.欄位>(有 N 列在轉換期間被並行修改,將於下次啟動重試)` | 直接重啟(舊程式仍在寫舊字串;全部換成新版後就不會再有) |
| `啟動遷移預算用完或等不到鎖` / `整體:啟動遷移整體預算在這一步之前已用完` | 調大 `OMIXOR_MIGRATE_BUDGET_MINUTES` 或直接重啟 |

### 重驗證與完成標記

- 開外鍵前的重驗證(已有完成標記、外鍵還沒加)與寫完成標記前的最終驗證都是**嚴格**的:非 NULL 而且不是有效 actor id 的值(**含空字串**)都算未轉換。轉換完成後舊程式又寫進 `assignee = ''`:開外鍵那次啟動作廢標記、重轉(空字串 → NULL),再加外鍵;外鍵不會在髒資料上加成 not valid 又 validate 失敗。
- 空白 → NULL 的批次以「是否還有符合條件的列」判斷結束,不是受影響列數(批次裡被併發修改的列會被跳過);寫完成標記前再驗證一次,有新寫入的舊值就不寫標記、不加外鍵,下次啟動再轉。
- 啟動彙總只列「欄位已轉完」的 actor 外鍵缺不缺(`fkSpecReady`);欄位沒轉完時不把 22 條外鍵都列成缺外鍵。

### 領域程式碼的狀態(C-2 共用地基這一步結束時)

store 的領域讀寫已是 actor id(C-2a~e 完成:model 欄位存 actor id、服務層 present 補名字),讀取橋(`whoSQL` 等)已刪除;寫入橋(`pg_actor_bridge.go`)只留給舊 jsonb 展開遷移與匯入。細節見 `ACTOR_CONTRACT.md`。已知與舊行為的差異(都是 actor 身分合一的自然結果):email 一律以 `omixor_users.email`(註冊使用者)或 actor handle(小寫)輸出;`system` 署名併入 `sys_omixor` 輸出 `omixor`;`RenameIdentity` 變成不改資料(改名是改 actor handle)。

### 不支援回滾

欄位被原地換成 actor id、空字串改 NULL、欄位改名、舊的複製來源欄位 drop 之後無法還原(舊字串沒有備份);要回滾只能從升級前的備份還原(同 §1)。

### 怎麼驗證

- 看啟動 log:`[遷移] actor 欄位:完成 22 / 22 欄,略過 0 欄`;每個 `sys_legacy_*` 對照都有一行 `[遷移] 舊值對照:…`(原值、組織、筆數、原因),第一次在有大量舊資料的資料庫跑完要檢查這些與 `[遷移警告]`,作為對照規則有沒有漏的證據。
- 查 `omixor_actor_aliases`:每個 `sys_legacy_*` 是哪個舊值。
- 孤兒:`OrphanReport()`(含 22 條 actor 外鍵)或每欄 `select count(*) from t x where x.col is not null and not exists (select 1 from omixor_actors a where a.id = x.col)` 必須 0。
- 還沒轉完的欄位:啟動會失敗並列出欄位與原因;啟動彙總也會列「尚未轉成 actor id 的欄位」(查欄位註解標記)。
- 測試:`go test ./store -run 'TestActorColumns|TestActorLegacyStructure|TestActorFK|TestResolveLegacy|TestBootstrapSchema|TestActorBatchUpdate'`(獨立審查修正的測試在 `pg_actor_columns_fx1_test.go`)(需要 PostgreSQL,見 `storetest/testdb`)。舊結構的樣本由測試輔助 `legacyActorStructure`(`pg_actor_final_test.go`)把空的全新資料庫還原成舊結構產生。


## ISS-37 子項:`omixor_issue_items`(Issue 子項,方案 B 獨立表)

**只加不改,沒有資料搬移,不重寫任何既有大表。** 全部在 `bootstrapSchema` / `migrateForeignKeys` 內冪等完成:

| 項目 | 內容 |
|---|---|
| 新表 `omixor_issue_items` | `create table if not exists`(直接是最終結構,fresh 與舊庫補建後結構一致,結構指紋測試 `TestIssueItemsMigrationStructure`)。主鍵 `(org, id)`(id = UUIDv7);唯一 `(org, issue_id, seq)`;`status` check(todo/doing/blocked/done/canceled);`wait_for_kind` check(空/user_decision/item/external);`files text[]`、`evidence jsonb`。建表時就有的外鍵:`(org, issue_id) → ops_issues` **cascade**(Issue 永久刪除時子項跟著刪)、`(org, depends_on) →` 本表 **set null (depends_on)**、`(org, promoted_issue_id) → ops_issues` **set null (promoted_issue_id)**(皆 on update cascade) |
| 索引 | `omixor_issue_items_issue (org, issue_id)`、`omixor_issue_items_assignee (org, assignee)`(`pgSchemaIndexes`) |
| actor 外鍵(`fkSpecs`,先 not valid 再背景 validate) | `fk_issue_items_assignee`(set null)、`fk_issue_items_claimed_by`(set null)、`fk_issue_items_created_by`(restrict)→ `omixor_actors(id)`;新表直接存 actor id,不走「誰」欄位轉換引擎,登記在 `NewTableActorFields`。另 `omixor_issue_items.org → omixor_orgs` cascade(`orgFKTables`)。fkSpecs 共 +4 條(關聯 +3、org +1) |
| `omixor_agent_tasks.item_id text` | `pgSchemaColumns` 補的可 NULL 欄位(可 NULL、無預設值,PG 11 起不重寫表);**不設外鍵**:任務與子項互不 cascade,雙向回寫由 service 處理 |
| 編號序號 | `omixor_seq` 的 scope `item:<Issue 內部 id>`;`seqScopeMax` 取 `max(seq)` 自癒(序號列被清或落後時不撞唯一約束);Issue 永久刪除時 `DeleteIssue` 一併清序號列;`DeleteOrg` 清子項與序號 |

行為備註:

- 領取(`ClaimIssueItem`)是單句條件式更新:`status = 'todo'`,或 `doing` 且(租約空 / 已到期 / 持有者是自己〔續租〕);其餘回 `ErrLocked`(blocked、done、canceled 也回 `ErrLocked`)。
- 依賴(`depends_on`)在交易內檢查:必須是同一 Issue 的另一個子項、不可自指、不可成環;違反回 `ErrBadRef`(訊息含原因)。檢查時先鎖住該 Issue 的全部子項列,序列化並發修改。
- 回滾:只能整個還原備份;舊版程式不認得此表(不讀不寫),新表與 `item_id` 欄位留著無害。刪表前先確認沒有子項資料。
- 測試:`go test ./store -run 'IssueItems|TestPGFKAllInstalled|TestActorColumnsFKSpecCount'`(需要 PostgreSQL,見 `storetest/testdb`)。


## 待決定 / 需要協助:`omixor_decisions`

**只加不改,沒有資料搬移,不重寫任何既有表。** 全部在 `bootstrapSchema` / `migrateSeq` / `migrateForeignKeys` 內冪等完成:

| 項目 | 內容 |
|---|---|
| 新表 `omixor_decisions` | `create table if not exists`,直接是最終結構(fresh 與舊庫補建後結構一致,結構指紋測試 `TestDecisionsTableStructure`)。主鍵 `(org, id)`(id = `DEC-<n>`);`kind` check(decision/help)、`status` check(open/decided/withdrawn);`options`、`blocks` 是 jsonb;`created_by` not null、`decided_by` 可 NULL(都是 actor id);`source_issue` 可 NULL,存 Issue 的**內部 id**,建表時就有外鍵 `(org, source_issue) → ops_issues (org, id)` **set null (source_issue)**、on update cascade(Issue 永久刪除時清成空;進垃圾桶不清) |
| 索引 | `omixor_decisions_org_status (org, status)`、`omixor_decisions_org_issue (org, source_issue)`(`pgSchemaIndexes`);`(org, number) where number > 0` 部分唯一索引 `omixor_decisions_org_number`(`seqTables`,`migrateSeq` 用 concurrently 建,失敗只告警) |
| 編號序號 | `omixor_seq` 的 scope `decision`;`seqScopeMax` 取 `max(number)` 自癒;`DeleteOrg` 一併清 |
| actor 外鍵(`fkSpecs` 一般關聯,先 not valid 再背景 validate) | `fk_decisions_created_by`、`fk_decisions_decided_by` → `omixor_actors(id)` **restrict**;`omixor_decisions.org → omixor_orgs` cascade(`orgFKTables`)。因為 `omixor_actors` 在 `pgSchema` 之後才建,所以和 `omixor_project_members` 一樣由 `migrateForeignKeys` 補 |
| 為什麼不進 `ActorColumns` | `ActorColumns` / `IdentityFields` 描述「舊字串 → actor id」的轉換(舊結構、`legacyActorStructure` 還原測試、22 條外鍵計數);新表從來沒有舊字串,直接以最終結構建立。「誰」欄位登記在 `identity_fields.go:NewTableActorFields`(測試確認外鍵存在),`pg_actor_columns_test.go:baseFKSpecs` 的關聯條數同步 +2 |
| `RenameIdentity` | 不含這張表(欄位是 actor id,不是字串身分) |

回滾:只能整個還原備份;舊版程式不認得此表(不讀不寫),新表留著無害。測試:`go test ./store -run 'TestStoreSuite/pg/Decisions|TestDecisions|TestStoreSuite/pg/DeleteOrgCascade|TestActorFKSpecsRegistered'`(需要 PostgreSQL,見 `storetest/testdb`)。


## OAuth(Claude 自訂 connector 唯讀連 MCP):`omixor_oauth_clients`、`omixor_oauth_tokens`

**只加不改,沒有資料搬移,不重寫任何既有表,不動 `omixor_tokens`(`omx_` 使用者 token 的語意完全不變)。** 全部在 `bootstrapSchema` 內冪等完成:

| 項目 | 內容 |
|---|---|
| 新表 `omixor_oauth_clients` | `create table if not exists`,直接是最終結構。主鍵 `client_id`;`client_name`、`redirect_uris text[]`、`grant_types text[]`、`created_at`。動態註冊(RFC 7591)的用戶端,總數上限 200(`CreateOAuthClient` 在 advisory lock 的交易內檢查;滿了先淘汰「註冊超過 1 小時、沒有簽發過授權碼或 token」的) |
| 新表 `omixor_oauth_tokens` | 同上。主鍵 `id`;`kind` check(csrf / code / access / refresh);`token_hash bytea` 唯一(只存雜湊,原文不存);`client_id` 外鍵 → `omixor_oauth_clients` **cascade**;`user_id` 可 NULL(csrf 沒有)外鍵 → `omixor_users` **cascade**;`org`、`scope`、`resource`(綁定的 MCP 網址)、`redirect_uri`、`code_challenge`(PKCE)、`family_id`、`created_at`、`expires_at`、`used_at`(code / refresh / csrf 用過)、`revoked_at`。`org` 不設外鍵(組織刪除後 token 仍存在但授權的組織已不存在,連線時由 service 的成員檢查擋掉) |
| 索引 | `omixor_oauth_tokens_family`(`family_id`,`family_id <> ''` 部分索引,家族撤銷用)、`omixor_oauth_tokens_expires`(`expires_at`,清理用),都在 `pgSchemaIndexes`(缺才建) |
| 清理 | 產生 CSRF token 時順便(最多每分鐘一次)刪掉過期超過一天的列(`PurgeOAuthTokens`) |
| 「用過一次」 | `ExchangeOAuthToken`:同一個交易內條件式 `update … set used_at where used_at is null and revoked_at is null and expires_at > now` 再新增新列;多個實例同時換同一個 code / refresh 只有一個成功 |
| 外鍵驗證、`fkSpecs` | 不進:兩條外鍵都在建表語句裡(新表,空表),`TestPGFKAllInstalled` 等外鍵計數不變 |

回滾:只能整個還原備份;舊版程式不認得這兩張表(不讀不寫),留著無害;刪表前先確認沒有仍在使用的授權(刪了就是所有 Claude connector 要重新授權)。測試:`go test ./store -run TestOAuth`(需要 PostgreSQL,見 `storetest/testdb`)。
