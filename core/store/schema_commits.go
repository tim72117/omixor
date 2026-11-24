package store

// schema_commits.go commits 三張表(ISS-69,設計見 docs/design-commits.md)的結構:全新資料庫(pgSchema 末尾)與
// 結構異動步驟 2(schema_steps.go 的 commits)用同一段 SQL,所以兩條路徑建出的結構一定一致。
// 步驟 2 在 Issue 表還叫 ops_issues 的資料庫上執行(步驟 3 才改名),所以步驟 2 的 SQL 要把 omixor_issues 換回舊名(schema_steps.go 的 commitsStepSQL)。
// 全部冪等(create … if not exists、外鍵先 drop constraint if exists 再加),只新增表與索引,不動既有的表。
//
//	omixor_commits        commit 的 metadata(主鍵 (org, repo, sha);repo 是 model.CommitRepoKey 正規化後的字串(保留 github:/gitlab: 前綴),沒有 repos 表;
//	                      subject 空字串 = 只被連結引用過的「裸參照」;verified 回報來的是 false)
//	omixor_issue_commits  Issue 與 commit 的連結(主鍵 (org, issue_id, repo, sha);issue_id 是 Issue 內部 id,Issue 永久刪除時連結跟著刪;
//	                      (org, repo, sha) 外鍵指向 omixor_commits;linked_by 是 actor id,不設外鍵(不擋使用者與角色的刪除))
//	omixor_repo_sync      每個 repo 最近一次回報的位置(主鍵 (org, repo)),讓 CLI / App 只送新的
//
// reported_by、linked_by 存 actor id 字串、沒有外鍵:稽核性質的紀錄,不該擋住 actor 的退役或刪除;輸出時查不到名字就省略。
const commitsSchemaSQL = `
create table if not exists omixor_commits (
  org text not null,
  repo text not null,
  sha text not null,
  subject text not null default ''::text,
  body text not null default ''::text,
  author_name text not null default ''::text,
  authored_at timestamp with time zone,
  committed_at timestamp with time zone,
  branch text not null default ''::text,
  parents text[] not null default '{}'::text[],
  url text not null default ''::text,
  source text not null default 'api'::text,
  verified boolean not null default false,
  reported_by text not null default ''::text,
  reported_at timestamp with time zone not null default now(),
  constraint omixor_commits_pkey primary key (org, repo, sha),
  constraint omixor_commits_sha_check check ((sha ~ '^[0-9a-f]{40}$'::text))
);
create table if not exists omixor_issue_commits (
  org text not null,
  issue_id text not null,
  repo text not null,
  sha text not null,
  kind text not null,
  origin text not null,
  linked_by text not null default ''::text,
  linked_at timestamp with time zone not null default now(),
  note text not null default ''::text,
  constraint omixor_issue_commits_pkey primary key (org, issue_id, repo, sha),
  constraint omixor_issue_commits_kind_check check ((kind = ANY (ARRAY['mentions'::text, 'fixes'::text, 'found_in'::text, 'manual'::text]))),
  constraint omixor_issue_commits_origin_check check ((origin = ANY (ARRAY['auto'::text, 'manual'::text])))
);
create table if not exists omixor_repo_sync (
  org text not null,
  repo text not null,
  last_sha text not null default ''::text,
  last_at timestamp with time zone not null default now(),
  last_source text not null default ''::text,
  constraint omixor_repo_sync_pkey primary key (org, repo)
);
create index if not exists omixor_commits_repo_time ON omixor_commits using btree (org, repo, authored_at desc);
create index if not exists omixor_commits_sha ON omixor_commits using btree (org, sha text_pattern_ops);
create index if not exists omixor_issue_commits_issue ON omixor_issue_commits using btree (org, issue_id, linked_at desc);
create index if not exists omixor_issue_commits_commit ON omixor_issue_commits using btree (org, repo, sha);
alter table omixor_commits drop constraint if exists fk_omixor_commits_org;
alter table omixor_commits add constraint fk_omixor_commits_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_issue_commits drop constraint if exists fk_omixor_issue_commits_org;
alter table omixor_issue_commits add constraint fk_omixor_issue_commits_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_repo_sync drop constraint if exists fk_omixor_repo_sync_org;
alter table omixor_repo_sync add constraint fk_omixor_repo_sync_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_issue_commits drop constraint if exists omixor_issue_commits_org_issue_id_fkey;
alter table omixor_issue_commits add constraint omixor_issue_commits_org_issue_id_fkey foreign key (org, issue_id) references omixor_issues(org, id) on update cascade on delete cascade;
alter table omixor_issue_commits drop constraint if exists omixor_issue_commits_org_repo_sha_fkey;
alter table omixor_issue_commits add constraint omixor_issue_commits_org_repo_sha_fkey foreign key (org, repo, sha) references omixor_commits(org, repo, sha) on update cascade on delete cascade;
`
