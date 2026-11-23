package store

// 凍結的 baseline 外鍵(fkSpecs() 在 baseline 當下的輸出,含 22 條「誰」欄位外鍵)。固定副本,不要修改。

const baselineFKSQL = `
alter table omixor_agent_credentials add constraint fk_agent_credentials_task foreign key (org, task_id) references omixor_agent_tasks (org, id) on delete cascade;
alter table omixor_channel_messages add constraint fk_channel_messages_channel foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade;
alter table omixor_channel_reads add constraint fk_channel_reads_channel foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade;
alter table omixor_channels add constraint fk_channels_issue foreign key (org, issue_id) references ops_issues (org, id) on delete set null (issue_id) on update cascade;
alter table omixor_docs add constraint fk_docs_project foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict;
alter table ops_issues add constraint fk_issues_project foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict;
alter table omixor_cabinet_sections add constraint fk_cabinet_sections_project foreign key (org, project_ref) references omixor_projects (org, id) on delete cascade;
alter table omixor_cabinet_tabs add constraint fk_cabinet_tabs_section foreign key (org, project, section_id) references omixor_cabinet_sections (org, project, id) on delete cascade on update cascade;
alter table omixor_project_members add constraint fk_project_members_actor foreign key (actor_id) references omixor_actors (id) on delete cascade;
alter table omixor_decisions add constraint fk_decisions_created_by foreign key (created_by) references omixor_actors (id) on delete restrict;
alter table omixor_decisions add constraint fk_decisions_decided_by foreign key (decided_by) references omixor_actors (id) on delete restrict;
alter table omixor_issue_items add constraint fk_issue_items_assignee foreign key (assignee) references omixor_actors (id) on delete set null;
alter table omixor_issue_items add constraint fk_issue_items_claimed_by foreign key (claimed_by) references omixor_actors (id) on delete set null;
alter table omixor_issue_items add constraint fk_issue_items_created_by foreign key (created_by) references omixor_actors (id) on delete restrict;
alter table omixor_agent_tasks add constraint fk_agent_tasks_agent foreign key (org, agent_id) references omixor_agents (org, id) on delete restrict;
alter table ops_issues add constraint fk_ops_issues_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_attachments add constraint fk_omixor_attachments_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_agents add constraint fk_omixor_agents_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_agent_tasks add constraint fk_omixor_agent_tasks_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_agent_credentials add constraint fk_omixor_agent_credentials_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_cabinet_layouts add constraint fk_omixor_cabinet_layouts_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_cabinet_sections add constraint fk_omixor_cabinet_sections_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_cabinet_tabs add constraint fk_omixor_cabinet_tabs_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_docs add constraint fk_omixor_docs_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_projects add constraint fk_omixor_projects_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_project_seq add constraint fk_omixor_project_seq_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_org_chains add constraint fk_omixor_org_chains_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_channels add constraint fk_omixor_channels_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_channel_messages add constraint fk_omixor_channel_messages_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_channel_reads add constraint fk_omixor_channel_reads_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_incidents add constraint fk_omixor_incidents_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_decisions add constraint fk_omixor_decisions_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_issue_items add constraint fk_omixor_issue_items_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_org_integrations add constraint fk_omixor_org_integrations_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table omixor_org_system add constraint fk_omixor_org_system_org foreign key (org) references omixor_orgs (id) on delete cascade;
alter table ops_issues add constraint fk_ops_issues_created_by foreign key (created_by) references omixor_actors (id) on delete restrict;
alter table ops_issues add constraint fk_ops_issues_assignee foreign key (assignee) references omixor_actors (id) on delete restrict;
alter table ops_issues add constraint fk_ops_issues_deleted_by foreign key (deleted_by) references omixor_actors (id) on delete restrict;
alter table ops_issue_comments add constraint fk_ops_issue_comments_author foreign key (author) references omixor_actors (id) on delete restrict;
alter table omixor_docs add constraint fk_omixor_docs_author foreign key (author) references omixor_actors (id) on delete restrict;
alter table omixor_agent_tasks add constraint fk_omixor_agent_tasks_created_by foreign key (created_by) references omixor_actors (id) on delete restrict;
alter table omixor_agent_tasks add constraint fk_omixor_agent_tasks_claimed_by foreign key (claimed_by) references omixor_actors (id) on delete restrict;
alter table omixor_agent_task_notes add constraint fk_omixor_agent_task_notes_author foreign key (author) references omixor_actors (id) on delete restrict;
alter table omixor_channels add constraint fk_omixor_channels_created_by foreign key (created_by) references omixor_actors (id) on delete restrict;
alter table omixor_channel_members add constraint fk_omixor_channel_members_member foreign key (member) references omixor_actors (id) on delete restrict;
alter table omixor_channel_messages add constraint fk_omixor_channel_messages_author foreign key (author) references omixor_actors (id) on delete restrict;
alter table omixor_channel_mentions add constraint fk_omixor_channel_mentions_member foreign key (member) references omixor_actors (id) on delete restrict;
alter table omixor_channel_reads add constraint fk_omixor_channel_reads_member foreign key (member) references omixor_actors (id) on delete restrict;
alter table omixor_org_chain_edges add constraint fk_omixor_org_chain_edges_from_actor foreign key (from_actor) references omixor_actors (id) on delete restrict;
alter table omixor_org_chain_edges add constraint fk_omixor_org_chain_edges_to_actor foreign key (to_actor) references omixor_actors (id) on delete restrict;
alter table omixor_incidents add constraint fk_omixor_incidents_commander foreign key (commander) references omixor_actors (id) on delete restrict;
alter table omixor_incidents add constraint fk_omixor_incidents_created_by foreign key (created_by) references omixor_actors (id) on delete restrict;
alter table omixor_incident_events add constraint fk_omixor_incident_events_actor foreign key (actor) references omixor_actors (id) on delete restrict;
alter table omixor_org_integrations add constraint fk_omixor_org_integrations_updated_by foreign key (updated_by) references omixor_actors (id) on delete restrict;
alter table omixor_org_system add constraint fk_omixor_org_system_updated_by foreign key (updated_by) references omixor_actors (id) on delete restrict;
alter table omixor_server_features add constraint fk_omixor_server_features_updated_by foreign key (updated_by) references omixor_actors (id) on delete restrict;
alter table omixor_audit add constraint fk_omixor_audit_actor foreign key (actor) references omixor_actors (id) on delete restrict;
`
