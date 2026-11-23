package store

// 凍結的 baseline 結構(第 2 段,接在第 1 段後面)。固定副本,不要修改。

const baselineSchemaB = `
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
`
