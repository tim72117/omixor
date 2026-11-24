// pg_schema.go 全新資料庫的建表語句:直接建出「最終結構」(pgSchema)與固定的外鍵(fkSpecs,pg_fk.go)。
//
// 這份結構和 schema_manifest.json(golden)一致,由 TestSchemaManifestInSync 把關。一次性的資料遷移程式已移除;
// 之後新增欄位、表、索引走有版本的結構異動步驟(schema_steps.go,執行器在 pg_schema_boot.go):
// 空資料庫直接建這份最終結構,已有 omixor 表的資料庫依版本套用還沒做的步驟。
//
// 下面的「資料表說明」是各表的用途;「舊 jsonb 欄位與 split_at」仍在表裡(下一版 drop),程式只寫 split_at = now(),不再讀它。

package store

// PostgreSQL 的存放處。Issue 的表(ops_issues、ops_issue_comments)和 omixor-web 原本的一樣,正式資料直接接得上。
//
//	omixor_users / omixor_tokens / omixor_audit   使用者、token(只存雜湊)、操作紀錄
//	omixor_orgs / omixor_org_members              組織與成員(角色 admin、member)
//	ops_issues / ops_issue_comments               Issue 與留言(主鍵 (org, id),id 是 UUIDv7;pos 大的在前;key 是顯示編號 ISS-<n> / <專案代號>-<n>,
//	                                              (org, upper(key)) 唯一;number 是 key 在所屬範圍(project,'' = 組織層級)內的號碼;project_ref 產生欄位外鍵指向專案(restrict);
//	                                              version 樂觀鎖版本號;deleted_at 不是 NULL = 在垃圾桶(軟刪除);見 pg_issue.go)
//	omixor_issue_keys                             Issue 目前與過去用過的 key(主鍵 (org, key),key 一律存大寫;issue_id 外鍵 cascade)
//	omixor_issue_keys_retired                     不再發的 Issue key(主鍵 (org, key),大寫;永久刪除的 Issue 的 key、舊版孤兒參照的 key;發號跳過,見 pg_issue.go)
//	omixor_attachments                            Issue 的圖片附件(主鍵 (org, id),id 是內容雜湊)
//	omixor_agents                                 虛擬角色(主鍵 (org, id);docAccess、routines 在 omixor_agent_doc_access、omixor_agent_routines;舊 jsonb 欄位與 split_at 已不再寫,下一版 drop;見 pg_doc_agent_children.go)
//	omixor_agent_tasks                            角色任務(主鍵 (org, id) = T-<n>;備註、連結在子表 omixor_agent_task_notes / _links,見 pg_incident_task_children.go;舊的 links、notes jsonb 欄位不再寫)
//	omixor_agent_credentials                      任務綁定的角色憑證(只存 SHA-256 雜湊與到期時間;token_hash 是主鍵)
//	ops_issues.assignee                           指派給的角色 id(空字串 = 未指派)
//	ops_issues.repo                               Issue 所屬的程式碼倉庫(owner/name 或 group/sub/project,可帶 github:/gitlab:;空字串 = 未標記)
//	omixor_cabinet_layouts                        文件櫃結構的「存過」標記與版本號(主鍵 (org, project);project 空字串 = 未分專案;version 是樂觀鎖的版本號,每存一次 +1;
//	                                              layout(jsonb)與 split_at 是舊格式遺留,已不再寫,下一版 drop;見 pg_cabinet.go)
//	omixor_cabinet_sections                       文件櫃的區塊(主鍵 (org, project, id);pos 是順序;文件的 section 欄位以字串指向 id,不加外鍵)
//	omixor_cabinet_tabs                           文件櫃的頁籤(主鍵 (org, project, path),網址在同一份結構內不重複;section_id 是所屬區塊,pos 是區塊內順序;
//	                                              code、grp 可以是 null;文件的 tab 欄位以字串指向 path)
//	omixor_docs                                   文件櫃的文件(主鍵 (org, id) = DOC-<n>,在組織內唯一;project 是所屬專案,空字串 = 未分專案;
//	                                              pos 大的在前;tags 在 omixor_doc_tags(舊 jsonb 欄位已不再寫,下一版 drop);tab 可以是 null)
//	omixor_projects                               專案(主鍵 (org, id) = PRJ-<n>;依 number 排序;key 是專案代號,(org, upper(key)) where key <> '' 唯一;方法在 pg_project.go)
//	omixor_project_seq                            專案最後發出去的號碼(主鍵 org;刪掉的號碼不再使用)
//	omixor_project_repos                          專案與程式碼倉庫的多對多連結(主鍵 (org, project, repo_key);repo_key 是 model.RepoKey 的標準寫法、repo 是連結時寫的樣子;
//	                                              (org, project) 外鍵指向專案,cascade。只用於預設值與篩選,不是權限來源;方法在 pg_project_repo.go)
//	omixor_project_members                        專案成員(主鍵 (org, project, actor_id);role 是 admin 或 member;(org, project) 外鍵指向專案 cascade,actor_id 外鍵指向 omixor_actors cascade(fk_project_members_actor,
//	                                              見 fkSpecs)。受限專案(omixor_projects.restricted)只有成員與管理員看得到;方法在 pg_project_members.go)
//	omixor_decisions                              待決定(decision)/ 需要協助(help)(主鍵 (org, id) = DEC-<n>;created_by、decided_by 是 actor id(fk_decisions_created_by / _decided_by,見 fkSpecs);
//	                                              (org, source_issue) 外鍵指向 ops_issues 的內部 id,Issue 永久刪除時清成 NULL;方法在 pg_decision.go)
//	omixor_oauth_clients、omixor_oauth_tokens     OAuth(Claude 自訂 connector 唯讀連 MCP):動態註冊的用戶端;授權碼、access、refresh、同意畫面的 CSRF token(只存雜湊;和 omixor_tokens 完全分開,方法在 pg_oauth.go,規則在 service/oauth.go)
//	omixor_issue_items                            Issue 子項(主鍵 (org, id) = uuidv7;(org, issue_id, seq) 唯一,seq 由 omixor_seq 的 item:<issueID> scope 發;
//	                                               assignee、claimed_by、created_by 是 actor id(fk_issue_items_*,見 fkSpecs);depends_on 自參照、issue_id cascade、promoted_issue_id set null 在建表時就有;
//	                                               新表直接是最終結構;task_id 是 omixor_agent_tasks.id 的字串參照,不設外鍵;規則在 service/issue_items.go)
//	omixor_commits、omixor_issue_commits、omixor_repo_sync   commits 與 Issue 的連結(ISS-69;定義與說明在 schema_commits.go,新資料庫與結構步驟 2 共用同一段 SQL)
//	omixor_seq                                    Issue、任務、文件、頻道、事件、待決定、頻道訊息(scope msg:<頻道 id>)最後發出去的號碼(主鍵 (org, scope);見 pg_seq.go)
//	omixor_org_chains                             組織鏈(主鍵 org;邊在 omixor_org_chain_edges;edges(jsonb)、split_at 是舊格式遺留,不再寫,下一版 drop;見 pg_channel_children.go)
//	omixor_channels                               訊息頻道(主鍵 (org, id) = CH-<n>;成員在 omixor_channel_members(舊 members jsonb 已不再寫,見 pg_channel_children.go);issue_id 沒綁是 NULL,同組織的 issue_id 不重複)
//	omixor_channel_messages                       頻道訊息(主鍵 (org, channel_id, id) = M-<n>,每個頻道各自編號;@ 的人在 omixor_channel_mentions(舊 mentions jsonb 已不再寫))
//	omixor_channel_reads                          已讀(主鍵 (org, channel_id, member);last_read 是讀到的訊息號碼)
//	omixor_incidents                              事件(主鍵 (org, id) = INC-<n>;pos 大的在前;純量欄位在本表、服務/後續工作/告警/時間軸在 omixor_incident_* 子表,見 pg_incident_task_children.go;舊的 data jsonb 不再寫;方法在 pg_incident.go)
//	omixor_org_integrations                       組織設定文件(選用模組的設定,例如 GitHub、GitLab 模組的 github、gitlab)與外部 MCP 登記(主鍵 (org, kind),kind 是 github、gitlab、mcp;設定存在 data jsonb,方法在 pg_integration.go、pg_mcp.go)
//	omixor_org_system                             組織的系統設定(功能開關;主鍵 org;有改過的開關存在 features jsonb,方法在 pg_system.go)
//	omixor_server_features                        系統層的功能設定(整台伺服器一列,主鍵 id 固定是 'server';有改過的開關存在 features jsonb,方法在 pg_system.go)
//
// 「誰」欄位(22 個:created_by、author、assignee、member、from_actor/to_actor、commander、updated_by、事件的 actor、稽核的 actor…)存 actor id
// (text,可否 NULL 見 identity_fields.go:ActorColumns),外鍵(references omixor_actors(id))在 fkSpecs(pg_fk.go)。欄位契約見 ACTOR_CONTRACT.md。
//
// 外鍵與刪除規則(角色→任務 restrict、頻道→Issue set null、各表的 org cascade 等)是 fkSpecs 的 fk_* 外鍵,建表之後才加(omixor_actors 等被參照的表要先在);
// 其餘外鍵與唯一、檢查約束就在這份建表語句裡。頻道的 issue_id 沒綁是 NULL、文件與區塊的 project 另有產生欄位 project_ref,細節見 pg_fk.go 檔頭
const pgSchema = `
create table omixor_actor_aliases (
  alias text not null,
  actor_id text not null,
  created_at timestamp with time zone not null default now(),
  constraint omixor_actor_aliases_pkey primary key (alias)
);
create table omixor_actors (
  id text not null,
  kind text not null,
  org text,
  name text not null,
  handle text not null,
  avatar text not null default ''::text,
  owner text,
  disabled_at timestamp with time zone,
  created_at timestamp with time zone not null default now(),
  constraint omixor_actors_check check ((((kind = 'user'::text) AND (id ~~ 'usr\_%'::text) AND (org IS NULL)) OR ((kind = 'agent'::text) AND (id ~~ 'agt\_%'::text) AND (org IS NOT NULL)) OR ((kind = 'system'::text) AND (id ~~ 'sys\_%'::text) AND (org IS NULL)))),
  constraint omixor_actors_check1 check (((kind <> 'user'::text) OR (handle = lower(handle)))),
  constraint omixor_actors_check2 check (((owner IS NULL) OR (kind = 'agent'::text))),
  constraint omixor_actors_handle_check check ((btrim(handle) <> ''::text)),
  constraint omixor_actors_id_kind_key unique (id, kind),
  constraint omixor_actors_kind_check check ((kind = ANY (ARRAY['user'::text, 'agent'::text, 'system'::text]))),
  constraint omixor_actors_pkey primary key (id)
);
create table omixor_agent_credentials (
  token_hash bytea not null,
  org text not null,
  agent_id text not null,
  task_id text not null,
  issued_at timestamp with time zone not null,
  expires_at timestamp with time zone not null,
  constraint omixor_agent_credentials_pkey primary key (token_hash)
);
create table omixor_agent_doc_access (
  org text not null,
  agent_id text not null,
  access_key text not null,
  level text not null,
  constraint omixor_agent_doc_access_pkey primary key (org, agent_id, access_key)
);
create table omixor_agent_routines (
  org text not null,
  agent_id text not null,
  id text not null,
  pos integer not null,
  title text not null default ''::text,
  instructions text not null default ''::text,
  kind text not null default ''::text,
  enabled boolean not null default false,
  every_hours integer not null default 0,
  hour integer not null default 0,
  minute integer not null default 0,
  weekday integer not null default 0,
  priority text not null default ''::text,
  created_at timestamp with time zone,
  last_run_at timestamp with time zone,
  constraint omixor_agent_routines_pkey primary key (org, agent_id, id)
);
create table omixor_agent_task_links (
  org text not null,
  task_id text not null,
  pos integer not null,
  url text not null,
  constraint omixor_agent_task_links_pkey primary key (org, task_id, pos)
);
create table omixor_agent_task_notes (
  org text not null,
  task_id text not null,
  pos integer not null,
  id text not null default ''::text,
  author text,
  text text not null default ''::text,
  at timestamp with time zone,
  constraint omixor_agent_task_notes_pkey primary key (org, task_id, pos)
);
create table omixor_agent_tasks (
  org text not null,
  id text not null,
  number integer not null,
  agent_id text not null,
  title text not null,
  instructions text not null default ''::text,
  priority text not null default 'normal'::text,
  status text not null,
  result text not null default ''::text,
  links jsonb not null default '[]'::jsonb,
  notes jsonb not null default '[]'::jsonb,
  routine_id text,
  created_by text,
  created_at timestamp with time zone not null,
  updated_at timestamp with time zone not null,
  claimed_at timestamp with time zone,
  completed_at timestamp with time zone,
  locked_until timestamp with time zone,
  claimed_by text,
  item_id text,
  split_at timestamp with time zone,
  constraint omixor_agent_tasks_pkey primary key (org, id),
  constraint omixor_agent_tasks_status_check check ((status = ANY (ARRAY['queued'::text, 'inProgress'::text, 'blocked'::text, 'done'::text, 'canceled'::text])))
);
create table omixor_agents (
  org text not null,
  id text not null,
  name text not null,
  role text not null default ''::text,
  instructions text not null default ''::text,
  active boolean not null default true,
  symbol text not null default ''::text,
  color text not null default ''::text,
  doc_access jsonb not null default '{}'::jsonb,
  routines jsonb not null default '[]'::jsonb,
  created_at timestamp with time zone not null default now(),
  split_at timestamp with time zone,
  actor_id text,
  constraint omixor_agents_actor_id_key unique (actor_id),
  constraint omixor_agents_pkey primary key (org, id)
);
create table omixor_attachments (
  org text not null,
  id text not null,
  data bytea not null,
  created_at timestamp with time zone not null default now(),
  constraint omixor_attachments_pkey primary key (org, id)
);
create table omixor_audit (
  id bigserial,
  at timestamp with time zone not null default now(),
  actor text,
  email text not null default ''::text,
  source text not null default ''::text,
  action text not null,
  org text not null default ''::text,
  detail jsonb,
  constraint omixor_audit_pkey primary key (id)
);
create table omixor_cabinet_layouts (
  org text not null,
  layout jsonb not null default '[]'::jsonb,
  updated_at timestamp with time zone not null default now(),
  project text not null default ''::text,
  split_at timestamp with time zone,
  version bigint not null default 0,
  constraint omixor_cabinet_layouts_pkey primary key (org, project)
);
create table omixor_cabinet_sections (
  org text not null,
  project text not null default ''::text,
  id text not null,
  pos integer not null,
  label text not null,
  path text not null default ''::text,
  project_ref text generated always as (NULLIF(project, ''::text)) stored,
  constraint omixor_cabinet_sections_pkey primary key (org, project, id)
);
create table omixor_cabinet_tabs (
  org text not null,
  project text not null default ''::text,
  section_id text not null,
  path text not null,
  pos integer not null,
  code text,
  grp text,
  label text not null,
  constraint omixor_cabinet_tabs_pkey primary key (org, project, path)
);
create table omixor_channel_members (
  org text not null,
  channel_id text not null,
  member text not null,
  pos integer not null,
  constraint omixor_channel_members_pkey primary key (org, channel_id, member)
);
create table omixor_channel_mentions (
  org text not null,
  channel_id text not null,
  message_id text not null,
  member text not null,
  pos integer not null,
  constraint omixor_channel_mentions_pkey primary key (org, channel_id, message_id, member)
);
create table omixor_channel_messages (
  org text not null,
  channel_id text not null,
  id text not null,
  number integer not null,
  author text not null,
  text text not null,
  mentions jsonb not null default '[]'::jsonb,
  at timestamp with time zone not null,
  split_at timestamp with time zone,
  constraint omixor_channel_messages_pkey primary key (org, channel_id, id)
);
create table omixor_channel_reads (
  org text not null,
  channel_id text not null,
  member text not null,
  last_read integer not null default 0,
  at timestamp with time zone not null,
  constraint omixor_channel_reads_pkey primary key (org, channel_id, member)
);
create table omixor_channels (
  org text not null,
  id text not null,
  number integer not null,
  name text not null,
  topic text not null default ''::text,
  issue_id text,
  members jsonb not null default '[]'::jsonb,
  created_by text,
  created_at timestamp with time zone not null,
  updated_at timestamp with time zone not null,
  split_at timestamp with time zone,
  constraint omixor_channels_pkey primary key (org, id)
);
create table omixor_decisions (
  org text not null,
  id text not null,
  number integer not null,
  kind text not null,
  title text not null,
  body text not null default ''::text,
  options jsonb not null default '[]'::jsonb,
  status text not null,
  answer text not null default ''::text,
  answer_note text not null default ''::text,
  evidence text not null default ''::text,
  source_issue text,
  blocks jsonb not null default '[]'::jsonb,
  created_by text not null,
  decided_by text,
  created_at timestamp with time zone not null,
  decided_at timestamp with time zone,
  updated_at timestamp with time zone not null,
  constraint omixor_decisions_kind_check check ((kind = ANY (ARRAY['decision'::text, 'help'::text]))),
  constraint omixor_decisions_pkey primary key (org, id),
  constraint omixor_decisions_status_check check ((status = ANY (ARRAY['open'::text, 'decided'::text, 'withdrawn'::text])))
);
create table omixor_doc_tags (
  org text not null,
  doc_id text not null,
  tag text not null,
  pos integer not null,
  constraint omixor_doc_tags_pkey primary key (org, doc_id, tag)
);
create table omixor_docs (
  org text not null,
  id text not null,
  number integer not null,
  pos bigint not null,
  title text not null,
  body text not null default ''::text,
  section text not null,
  tab text,
  tags jsonb not null default '[]'::jsonb,
  author text,
  created_at timestamp with time zone not null,
  updated_at timestamp with time zone not null,
  project text not null default ''::text,
  split_at timestamp with time zone,
  project_ref text generated always as (NULLIF(project, ''::text)) stored,
  constraint omixor_docs_pkey primary key (org, id)
);
create table omixor_incident_alerts (
  org text not null,
  incident_id text not null,
  pos integer not null,
  alert_id text not null,
  constraint omixor_incident_alerts_pkey primary key (org, incident_id, pos)
);
create table omixor_incident_events (
  org text not null,
  incident_id text not null,
  pos integer not null,
  id text not null default ''::text,
  at timestamp with time zone,
  actor text,
  kind text not null default ''::text,
  text text not null default ''::text,
  constraint omixor_incident_events_pkey primary key (org, incident_id, pos)
);
create table omixor_incident_follow_ups (
  org text not null,
  incident_id text not null,
  pos integer not null,
  ref text not null,
  constraint omixor_incident_follow_ups_pkey primary key (org, incident_id, pos)
);
create table omixor_incident_services (
  org text not null,
  incident_id text not null,
  pos integer not null,
  name text not null,
  constraint omixor_incident_services_pkey primary key (org, incident_id, pos)
);
create table omixor_incidents (
  org text not null,
  id text not null,
  number integer not null,
  pos bigint not null,
  status text not null,
  started_at timestamp with time zone not null,
  data jsonb not null default '{}'::jsonb,
  title text not null default ''::text,
  sev integer not null default 0,
  commander text,
  resolved_at timestamp with time zone,
  impact text not null default ''::text,
  ai_summary text,
  created_by text,
  split_at timestamp with time zone,
  constraint omixor_incidents_pkey primary key (org, id)
);
create table omixor_issue_items (
  org text not null,
  id text not null,
  issue_id text not null,
  seq integer not null,
  title text not null,
  status text not null,
  assignee text,
  depends_on text,
  files text[] not null default '{}'::text[],
  acceptance text not null default ''::text,
  evidence jsonb,
  block_reason text not null default ''::text,
  wait_for_kind text not null default ''::text,
  wait_for text not null default ''::text,
  claimed_by text,
  locked_until timestamp with time zone,
  task_id text,
  promoted_issue_id text,
  created_by text not null,
  created_at timestamp with time zone not null,
  updated_at timestamp with time zone not null,
  completed_at timestamp with time zone,
  constraint omixor_issue_items_org_issue_id_seq_key unique (org, issue_id, seq),
  constraint omixor_issue_items_pkey primary key (org, id),
  constraint omixor_issue_items_status_check check ((status = ANY (ARRAY['todo'::text, 'doing'::text, 'blocked'::text, 'done'::text, 'canceled'::text]))),
  constraint omixor_issue_items_wait_for_kind_check check ((wait_for_kind = ANY (ARRAY[''::text, 'user_decision'::text, 'item'::text, 'external'::text])))
);
create table omixor_issue_keys (
  org text not null,
  key text not null,
  issue_id text not null,
  created_at timestamp with time zone not null default now(),
  constraint omixor_issue_keys_pkey primary key (org, key)
);
create table omixor_issue_keys_retired (
  org text not null,
  key text not null,
  reason text not null,
  created_at timestamp with time zone not null default now(),
  constraint omixor_issue_keys_retired_pkey primary key (org, key)
);
create table omixor_oauth_clients (
  client_id text not null,
  client_name text not null default ''::text,
  redirect_uris text[] not null,
  grant_types text[] not null default '{}'::text[],
  created_at timestamp with time zone not null default now(),
  constraint omixor_oauth_clients_pkey primary key (client_id)
);
create table omixor_oauth_tokens (
  id text not null,
  kind text not null,
  token_hash bytea not null,
  client_id text not null,
  user_id text,
  org text not null default ''::text,
  scope text not null default ''::text,
  resource text not null default ''::text,
  redirect_uri text not null default ''::text,
  code_challenge text not null default ''::text,
  family_id text not null default ''::text,
  created_at timestamp with time zone not null default now(),
  expires_at timestamp with time zone not null,
  used_at timestamp with time zone,
  revoked_at timestamp with time zone,
  constraint omixor_oauth_tokens_kind_check check ((kind = ANY (ARRAY['csrf'::text, 'code'::text, 'access'::text, 'refresh'::text]))),
  constraint omixor_oauth_tokens_pkey primary key (id),
  constraint omixor_oauth_tokens_token_hash_key unique (token_hash)
);
create table omixor_org_chain_edges (
  org text not null,
  pos integer not null,
  from_actor text not null,
  to_actor text not null,
  label text not null default ''::text,
  constraint omixor_org_chain_edges_pkey primary key (org, pos)
);
create table omixor_org_chains (
  org text not null,
  edges jsonb not null default '[]'::jsonb,
  updated_at timestamp with time zone not null default now(),
  split_at timestamp with time zone,
  constraint omixor_org_chains_pkey primary key (org)
);
create table omixor_org_integrations (
  org text not null,
  kind text not null,
  data jsonb not null,
  updated_at timestamp with time zone not null,
  updated_by text,
  constraint omixor_org_integrations_pkey primary key (org, kind)
);
create table omixor_org_members (
  org text not null,
  user_id text not null,
  role text not null,
  added_at timestamp with time zone not null default now(),
  constraint omixor_org_members_pkey primary key (org, user_id),
  constraint omixor_org_members_role_check check ((role = ANY (ARRAY['admin'::text, 'member'::text])))
);
create table omixor_org_system (
  org text not null,
  features jsonb not null,
  updated_at timestamp with time zone not null,
  updated_by text,
  constraint omixor_org_system_pkey primary key (org)
);
create table omixor_orgs (
  id text not null,
  name text not null,
  created_at timestamp with time zone not null default now(),
  symbol text not null default ''::text,
  color text not null default ''::text,
  note text not null default ''::text,
  constraint omixor_orgs_pkey primary key (id)
);
create table omixor_project_members (
  org text not null,
  project text not null,
  actor_id text not null,
  role text not null,
  created_at timestamp with time zone not null default now(),
  constraint omixor_project_members_pkey primary key (org, project, actor_id),
  constraint omixor_project_members_role_check check ((role = ANY (ARRAY['admin'::text, 'member'::text])))
);
create table omixor_project_repos (
  org text not null,
  project text not null,
  repo_key text not null,
  repo text not null,
  created_at timestamp with time zone not null default now(),
  constraint omixor_project_repos_pkey primary key (org, project, repo_key)
);
create table omixor_project_seq (
  org text not null,
  last integer not null,
  constraint omixor_project_seq_pkey primary key (org)
);
create table omixor_projects (
  org text not null,
  id text not null,
  number integer not null,
  name text not null,
  description text not null default ''::text,
  created_at timestamp with time zone not null,
  updated_at timestamp with time zone not null,
  key text not null default ''::text,
  restricted boolean not null default false,
  constraint omixor_projects_pkey primary key (org, id)
);
create table omixor_seq (
  org text not null,
  scope text not null,
  last integer not null,
  constraint omixor_seq_pkey primary key (org, scope)
);
create table omixor_server_features (
  id text not null,
  features jsonb not null,
  updated_at timestamp with time zone not null,
  updated_by text,
  constraint omixor_server_features_id_check check ((id = 'server'::text)),
  constraint omixor_server_features_pkey primary key (id)
);
create table omixor_tokens (
  id text not null,
  token_hash bytea not null,
  user_id text not null,
  device text not null default ''::text,
  created_at timestamp with time zone not null default now(),
  last_used_at timestamp with time zone,
  expires_at timestamp with time zone,
  revoked_at timestamp with time zone,
  constraint omixor_tokens_pkey primary key (id),
  constraint omixor_tokens_token_hash_key unique (token_hash)
);
create table omixor_users (
  id text not null,
  evoleap_id text,
  email text not null,
  name text not null default ''::text,
  role text not null,
  disabled boolean not null default false,
  created_at timestamp with time zone not null default now(),
  last_login_at timestamp with time zone,
  actor_id text,
  kind text not null default 'user'::text,
  constraint omixor_users_email_key unique (email),
  constraint omixor_users_evoleap_id_key unique (evoleap_id),
  constraint omixor_users_kind_check check ((kind = 'user'::text)),
  constraint omixor_users_pkey primary key (id),
  constraint omixor_users_role_check check ((role = ANY (ARRAY['admin'::text, 'member'::text])))
);
create table ops_issue_comments (
  org text not null,
  issue_id text not null,
  pos integer not null,
  id text not null,
  author text not null,
  text text not null,
  at timestamp with time zone not null,
  constraint ops_issue_comments_pkey primary key (org, issue_id, pos)
);
create table ops_issues (
  org text not null,
  id text not null,
  number integer not null,
  pos bigint not null,
  title text not null,
  body text not null default ''::text,
  status text not null,
  priority text not null,
  labels text[] not null default '{}'::text[],
  source text not null default ''::text,
  link text not null default ''::text,
  created_by text not null,
  created_at timestamp with time zone not null,
  updated_at timestamp with time zone not null,
  closed_at timestamp with time zone,
  assignee text,
  repo text not null default ''::text,
  project text not null default ''::text,
  key text not null default ''::text,
  version integer not null default 1,
  deleted_at timestamp with time zone,
  deleted_by text,
  project_ref text generated always as (NULLIF(project, ''::text)) stored,
  constraint ops_issues_pkey primary key (org, id),
  constraint ops_issues_priority_check check ((priority = ANY (ARRAY['urgent'::text, 'high'::text, 'normal'::text, 'low'::text]))),
  constraint ops_issues_status_check check ((status = ANY (ARRAY['open'::text, 'closed'::text])))
);
CREATE unique INDEX omixor_actors_agent_handle ON omixor_actors using btree (org, handle) where ((kind = 'agent'::text) AND (disabled_at IS NULL));
CREATE unique INDEX omixor_actors_id_org_handle ON omixor_actors using btree (id, org, handle);
CREATE unique INDEX omixor_actors_system_handle ON omixor_actors using btree (handle) where (kind = 'system'::text);
CREATE unique INDEX omixor_actors_user_handle ON omixor_actors using btree (lower(handle)) where (kind = 'user'::text);
CREATE unique INDEX omixor_agent_credentials_task ON omixor_agent_credentials using btree (org, task_id);
CREATE unique INDEX omixor_agent_tasks_org_number ON omixor_agent_tasks using btree (org, number) where (number > 0);
CREATE unique INDEX omixor_channel_messages_org_chan_number ON omixor_channel_messages using btree (org, channel_id, number) where (number > 0);
CREATE unique INDEX omixor_channels_issue ON omixor_channels using btree (org, issue_id) where (issue_id <> ''::text);
CREATE unique INDEX omixor_channels_org_number ON omixor_channels using btree (org, number) where (number > 0);
CREATE unique INDEX omixor_decisions_org_number ON omixor_decisions using btree (org, number) where (number > 0);
CREATE unique INDEX omixor_docs_org_number ON omixor_docs using btree (org, number) where (number > 0);
CREATE unique INDEX omixor_incidents_org_number ON omixor_incidents using btree (org, number) where (number > 0);
CREATE unique INDEX omixor_projects_org_key ON omixor_projects using btree (org, upper(key)) where (key <> ''::text);
CREATE unique INDEX omixor_users_actor_id ON omixor_users using btree (actor_id);
CREATE unique INDEX ops_issues_org_key ON ops_issues using btree (org, upper(key)) where (key <> ''::text);
CREATE unique INDEX ops_issues_org_number ON ops_issues using btree (org, project, number) where (number > 0);
-- 索引(建在外鍵之前:omixor_agents 的複合外鍵要指到 omixor_actors 的唯一索引)
create index omixor_actor_aliases_actor ON omixor_actor_aliases using btree (actor_id);
create index omixor_actors_agent_org ON omixor_actors using btree (org) where (kind = 'agent'::text);
create index omixor_agent_task_notes_id ON omixor_agent_task_notes using btree (org, task_id, id);
create index omixor_agent_tasks_agent ON omixor_agent_tasks using btree (org, agent_id, status);
create index omixor_audit_at ON omixor_audit using btree (at desc);
create index omixor_channel_members_member ON omixor_channel_members using btree (org, member);
create index omixor_channel_mentions_member ON omixor_channel_mentions using btree (org, member);
create index omixor_channel_messages_num ON omixor_channel_messages using btree (org, channel_id, number);
create index omixor_decisions_org_issue ON omixor_decisions using btree (org, source_issue);
create index omixor_decisions_org_status ON omixor_decisions using btree (org, status);
create index omixor_doc_tags_tag ON omixor_doc_tags using btree (org, tag);
create index omixor_docs_org_pos ON omixor_docs using btree (org, pos desc);
create index omixor_docs_org_project_ref ON omixor_docs using btree (org, project_ref);
create index omixor_incident_alerts_alert ON omixor_incident_alerts using btree (org, alert_id);
create index omixor_incident_events_id ON omixor_incident_events using btree (org, incident_id, id);
create index omixor_incident_follow_ups_ref ON omixor_incident_follow_ups using btree (org, ref);
create index omixor_incident_services_name ON omixor_incident_services using btree (org, name);
create index omixor_incidents_org_commander ON omixor_incidents using btree (org, commander);
create index omixor_incidents_org_pos ON omixor_incidents using btree (org, pos desc);
create index omixor_incidents_org_sev ON omixor_incidents using btree (org, sev);
create index omixor_incidents_org_status ON omixor_incidents using btree (org, status);
create index omixor_issue_items_assignee ON omixor_issue_items using btree (org, assignee);
create index omixor_issue_items_issue ON omixor_issue_items using btree (org, issue_id);
create index omixor_issue_keys_issue ON omixor_issue_keys using btree (org, issue_id);
create index omixor_oauth_tokens_expires ON omixor_oauth_tokens using btree (expires_at);
create index omixor_oauth_tokens_family ON omixor_oauth_tokens using btree (family_id) where (family_id <> ''::text);
create index omixor_project_members_actor ON omixor_project_members using btree (org, actor_id);
create index ops_issues_org_pos ON ops_issues using btree (org, pos desc);
-- 外鍵
alter table omixor_actor_aliases add constraint omixor_actor_aliases_actor_id_fkey foreign key (actor_id) references omixor_actors(id);
alter table omixor_actors add constraint omixor_actors_owner_fkey foreign key (owner) references omixor_actors(id) on delete restrict;
alter table omixor_agent_doc_access add constraint omixor_agent_doc_access_org_agent_id_fkey foreign key (org, agent_id) references omixor_agents(org, id) on update cascade on delete cascade;
alter table omixor_agent_routines add constraint omixor_agent_routines_org_agent_id_fkey foreign key (org, agent_id) references omixor_agents(org, id) on update cascade on delete cascade;
alter table omixor_agent_task_links add constraint omixor_agent_task_links_org_task_id_fkey foreign key (org, task_id) references omixor_agent_tasks(org, id) on update cascade on delete cascade;
alter table omixor_agent_task_notes add constraint omixor_agent_task_notes_org_task_id_fkey foreign key (org, task_id) references omixor_agent_tasks(org, id) on update cascade on delete cascade;
alter table omixor_agents add constraint omixor_agents_actor_fk foreign key (actor_id, org, id) references omixor_actors(id, org, handle);
alter table omixor_channel_members add constraint omixor_channel_members_org_channel_id_fkey foreign key (org, channel_id) references omixor_channels(org, id) on update cascade on delete cascade;
alter table omixor_channel_mentions add constraint omixor_channel_mentions_org_channel_id_message_id_fkey foreign key (org, channel_id, message_id) references omixor_channel_messages(org, channel_id, id) on update cascade on delete cascade;
alter table omixor_decisions add constraint omixor_decisions_org_source_issue_fkey foreign key (org, source_issue) references ops_issues(org, id) on update cascade on delete set null (source_issue);
alter table omixor_doc_tags add constraint omixor_doc_tags_org_doc_id_fkey foreign key (org, doc_id) references omixor_docs(org, id) on update cascade on delete cascade;
alter table omixor_incident_alerts add constraint omixor_incident_alerts_org_incident_id_fkey foreign key (org, incident_id) references omixor_incidents(org, id) on update cascade on delete cascade;
alter table omixor_incident_events add constraint omixor_incident_events_org_incident_id_fkey foreign key (org, incident_id) references omixor_incidents(org, id) on update cascade on delete cascade;
alter table omixor_incident_follow_ups add constraint omixor_incident_follow_ups_org_incident_id_fkey foreign key (org, incident_id) references omixor_incidents(org, id) on update cascade on delete cascade;
alter table omixor_incident_services add constraint omixor_incident_services_org_incident_id_fkey foreign key (org, incident_id) references omixor_incidents(org, id) on update cascade on delete cascade;
alter table omixor_issue_items add constraint omixor_issue_items_org_depends_on_fkey foreign key (org, depends_on) references omixor_issue_items(org, id) on update cascade on delete set null (depends_on);
alter table omixor_issue_items add constraint omixor_issue_items_org_issue_id_fkey foreign key (org, issue_id) references ops_issues(org, id) on update cascade on delete cascade;
alter table omixor_issue_items add constraint omixor_issue_items_org_promoted_issue_id_fkey foreign key (org, promoted_issue_id) references ops_issues(org, id) on update cascade on delete set null (promoted_issue_id);
alter table omixor_issue_keys add constraint omixor_issue_keys_org_issue_id_fkey foreign key (org, issue_id) references ops_issues(org, id) on update cascade on delete cascade;
alter table omixor_oauth_tokens add constraint omixor_oauth_tokens_client_id_fkey foreign key (client_id) references omixor_oauth_clients(client_id) on delete cascade;
alter table omixor_oauth_tokens add constraint omixor_oauth_tokens_user_id_fkey foreign key (user_id) references omixor_users(id) on delete cascade;
alter table omixor_org_chain_edges add constraint omixor_org_chain_edges_org_fkey foreign key (org) references omixor_org_chains(org) on update cascade on delete cascade;
alter table omixor_org_members add constraint omixor_org_members_org_fkey foreign key (org) references omixor_orgs(id);
alter table omixor_org_members add constraint omixor_org_members_user_id_fkey foreign key (user_id) references omixor_users(id);
alter table omixor_project_members add constraint omixor_project_members_org_project_fkey foreign key (org, project) references omixor_projects(org, id) on delete cascade;
alter table omixor_project_repos add constraint omixor_project_repos_org_project_fkey foreign key (org, project) references omixor_projects(org, id) on delete cascade;
alter table omixor_users add constraint fk_users_actor foreign key (actor_id, kind) references omixor_actors(id, kind) on delete restrict;
alter table omixor_tokens add constraint omixor_tokens_user_id_fkey foreign key (user_id) references omixor_users(id);
alter table ops_issue_comments add constraint ops_issue_comments_org_issue_id_fkey foreign key (org, issue_id) references ops_issues(org, id) on update cascade on delete cascade;
` + commitsSchemaSQL + schemaVersionDDL
