package store

// 凍結的 baseline 結構(第 1 段):版本 1 以前的資料庫(正式站、使用者本機)的結構,等於當初 omixor_schema_version 導入時的 pgSchema。
// 這是固定副本,不隨 pgSchema 演進;不含 omixor_schema_version(版本表是版本 1 之後由執行器建立)。見 schema_baseline_test.go。

const baselineSchemaA = `

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
`
