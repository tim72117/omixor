package mcp

import (
	"encoding/json"

	"omixor-core/model"
)

// 核心自己的工具規格(tools/list)與 initialize 資訊:給核心掛載的端點用(AgentEndpoint、AdminEndpoint)。
// omixor-web 的 /mcp 也用這裡的核心工具定義(加上 web 自己的工具,見 console/internal/mcp/handler.go 的 webSpec),
// Swift App 的 /mcp 則把核心工具轉給 core server 的管理端點,列出的也是這一份。核心工具的定義只在這裡維護。

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func pStr(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func pBool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func pInt(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func pEnum(desc string, v ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": v}
}
func pStrs(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
}
func pObjs(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "object"}}
}

// ToolDef 一個工具的規格
func ToolDef(name, title, desc string, readOnly bool, schema map[string]any) map[string]any {
	return map[string]any{
		"name": name, "title": title, "description": desc, "inputSchema": schema,
		"annotations": map[string]any{"title": title, "readOnlyHint": readOnly, "destructiveHint": false, "openWorldHint": false},
	}
}

// coreDef 核心工具的規格:標題取自登記表(model.CoreTools)的英文標題 TitleEN(給 LLM 看);沒登記的名稱直接 panic(啟動時就會發現)
func coreDef(name, desc string, readOnly bool, schema map[string]any) map[string]any {
	t, ok := model.CoreToolByName(name)
	if !ok {
		panic("mcp: tool " + name + " is not registered in model.CoreTools")
	}
	return ToolDef(name, t.TitleEN, desc, readOnly, schema)
}

// projectParam 文件櫃工具的 project 參數說明
const projectParam = "Project id (e.g. PRJ-1, see projects); omit = no project (the organization's original cabinet)"

// CoreToolDefs 核心提供的工具規格:組織、Issue、虛擬角色與任務、專案、文件櫃
func CoreToolDefs() []any {
	prio := []string{"urgent", "high", "normal", "low"}
	defs := []any{
		coreDef("orgs", "Lists the organizations you can use (current marks the organization this connection belongs to).", true, obj(map[string]any{})),
		coreDef("issue_create",
			"Saves a finding or to-do as an issue. Write the body in Markdown with the observed behavior, evidence (numbers, URLs, time range) and a suggestion. Search with issues first to avoid duplicates. Giving assignee (an agent id) automatically queues a task for that agent.", false,
			obj(map[string]any{
				"assignee": pStr("Id of the agent to assign to; a task is queued for it automatically (the agent must exist)"),
				"body":     pStr("Body (Markdown)"),
				"labels":   pStrs("Labels, e.g. [\"evoleap\", \"visitors\"]"),
				"link":     pStr("Related URL"),
				"priority": pEnum("Priority, default normal", prio...),
				"project":  pStr("Project id to put the issue in (e.g. PRJ-1; the project must have a key set first, and the number will be <key>-<n>). Omitted = if the repo is linked to exactly one project (see link_repos of project_save) it goes into that project, otherwise it stays at organization level (ISS-<n>); empty string = explicitly no project (organization level)"),
				"return":   pEnum("Response size: ack (default) = compact receipt {key, id, status, version, updatedAt, changed, comment_id?}; full = the whole issue including body and all comments", "ack", "full"),
				"repo":     pStr("Source code repository, e.g. owner/name (GitHub) or group/sub/project (GitLab); a github: or gitlab: prefix is allowed; only letters, digits and - _ . / : are accepted"),
				"source":   pStr("Where it was found, e.g. \"website visitors 7d\""),
				"title":    pStr("Title, one sentence describing the problem"),
			}, "title")),
		coreDef("report_problem",
			"Reports a problem with omixor itself (a tool that failed, behaved unexpectedly, or a missing capability) as an issue labeled mcp-feedback, with non-secret diagnostics attached automatically (server, endpoint, auth style, UTC time, client user-agent). Do NOT put tokens, passwords or credentials in any field; secret-looking text is masked automatically. Reporting the same tool and error again adds a comment to the existing open report instead of creating a duplicate (deduplicated=true). Use issue_create for ordinary findings and to-dos.", false,
			obj(map[string]any{
				"title":         pStr("One line summarizing the problem (max 200 characters)"),
				"what_happened": pStr("What you did and what actually happened (max 4000 characters)"),
				"expected":      pStr("What you expected to happen (max 4000 characters)"),
				"tool":          pStr("Name of the omixor MCP tool involved, e.g. issue_update"),
				"error":         pStr("The error text, verbatim (max 4000 characters)"),
				"severity":      pEnum("How bad it is: blocker (cannot continue; priority high), annoying (default; priority normal), idea (suggestion; priority low)", "blocker", "annoying", "idea"),
				"return":        pEnum("Response size: ack (default) = compact receipt {key, id, status, version, updatedAt, changed, comment_id?, deduplicated}; full = the whole issue plus deduplicated", "ack", "full"),
			}, "title")),
		coreDef("issue_update",
			"Updates an issue: change the title, body, priority, labels, repository, assignee or project (move), add a comment, or close/reopen it. Give only the fields to change. id may be a key or an internal id; the returned key is the display number.", false,
			obj(map[string]any{
				"add_labels":  pStrs("Labels to add"),
				"append_body": pStr("Text to append after the body"),
				"assignee":    pStr("Id of the agent to reassign to (empty string = unassign); a task is queued for the new agent automatically and the old agent's unclaimed tasks are canceled"),
				"body":        pStr("Replaces the body (Markdown)"),
				"comment":     pStr("Adds a comment"),
				"id":          pStr("Issue number: a key (e.g. ISS-3, APP-12; old keys also work) or an internal id"),
				"labels":      pStrs("Replaces all labels"),
				"priority":    pEnum("Priority", prio...),
				"project":     pStr("Move to this project (project id, e.g. PRJ-1; empty string = move back to organization level). A new key is issued and the old key still resolves"),
				"version":     pInt("The issue's current version (as returned by issues); when given, the version is checked and an error is returned if someone else changed the issue meanwhile (nothing is overwritten); omitted = no check"),
				"return":      pEnum("Response size: ack (default) = compact receipt {key, id, status, version (the NEW version), updatedAt, changed, comment_id?}; full = the whole issue including body and all comments", "ack", "full"),
				"repo":        pStr("Changes the source code repository (owner/name or group/sub/project, a github: or gitlab: prefix is allowed; empty string = clear the mark)"),
				"status":      pEnum("open reopens, closed closes", "open", "closed"),
				"title":       pStr("New title"),
			}, "id")),
		coreDef("schema_check",
			"Checks the database schema (read-only; omixor server administrators only; agent credentials are always rejected): compares the schema of a freshly migrated database with the actual schema of this database, table by table. Only system catalogs are queried: no data is scanned, no tables are locked, and no audit entry is written."+
				"Returns ok (true only if every table is ok, there are no missing or orphan tables, and the schema version is not behind), schemaVersion (the highest schema version recorded in the database), expectedVersion (the highest schema version this build knows; if schemaVersion is lower, schema changes are still unapplied and restarting the server applies them automatically), tables (per table: table, missingColumns, extraColumns, typeMismatches for type or nullability differences [column, expected, actual], primaryKeyMismatch [expected, actual; absent when they match], ok), "+
				"missingTables (tables that should exist but are missing from the database) and orphanTables (tables in the database with no matching definition); both sorted by name. "+
				"Any difference from a fresh database is listed.", true,
			obj(map[string]any{})),
		coreDef("issue_accept",
			"Accepts an issue: you become the issue's owner (assignee), the issue moves to \"in progress\", and a system comment is added. Use it when \"I own this and will analyze and break it down\"; usually there are no items yet at that point, and you then use issue_item_add to split it into items, each of which is claimed and executed with issue_item_claim (the two levels are not mutually exclusive). "+
				"Already accepted by you: no-op (idempotent); already accepted by someone else: 409 (ask them to release it, or hand it to an agent with the assignee of issue_update); issue closed: 409. "+
				"release=true releases it: clears the owner and returns the issue to pending (only the current acceptor or an organization admin; no-op if nobody has accepted it). Usable from both the human and the agent endpoints; the acceptor is always the caller.", false,
			obj(map[string]any{
				"id":      pStr("Issue number: a key (e.g. ISS-3, APP-12) or an internal id"),
				"release": pBool("true = release (clear the owner, return to pending); default false = accept"),
				"return":  pEnum("Response size: ack (default) = compact receipt {key, id, status, version, updatedAt, changed, assignee, accepted, message} (assignee = display name of the current owner, empty when nobody owns it; accepted = true when the issue is now accepted/in progress; changed lists only fields that really changed, empty for an idempotent repeat; status stays open while in progress); full = the whole issue including body and all comments plus message", "ack", "full"),
			}, "id")),
		coreDef("issues",
			"With id, returns the full issue (including comments); without id, lists the issues that match the filters (open ones only by default, sorted by priority and time). "+
				"Always show and reference issues by key (ISS-<n>, or <project key>-<n> inside a project); id is the internal id; both forms (including old keys) work as parameters. Deleted issues (trash) are not listed.", true,
			obj(map[string]any{
				"id":              pStr("Issue number: a key (e.g. ISS-3, APP-12) or an internal id"),
				"comments_limit":  pInt("With id: how many of the most recent comments to return (default 5; 0 or negative = no comments; a very large number = all). The result always has comments_total, comments_returned and comments_truncated"),
				"comments_before": pStr("With id: return only comments strictly older than this (a comment id from a previous result, or an RFC3339 timestamp); use it to page back through older comments"),
				"project":         pStr("List only this project's issues (project id, e.g. PRJ-1); \"-\" = organization level only; omitted = all"),
				"label":           pStr("List only issues with this label"),
				"limit":           pInt("Maximum number of results, default 30"),
				"query":           pStr("Searches title, body, comments, number and labels (case-insensitive; every whitespace-separated word must appear)"),
				"repo":            pStr("List only issues marked with this source code repository (case-insensitive; a github: or gitlab: prefix missing on one side is ignored)"),
				"status":          pEnum("open, closed or all, default open", "open", "closed", "all"),
			})),
		coreDef("agents",
			"Lists the organization's agents: id, name, role, instructions, routines, and the number of tasks per status. To work as an agent, read its instructions first, then claim a task with agent_task_claim to get the agent credential and connect to the agent endpoint.", true,
			obj(map[string]any{"id": pStr("Show only this agent")})),
		coreDef("agent_context",
			"Gets, in one call by agent id or name, everything this agent can read while working: its instructions, open tasks, the cabinet sections and tabs it can access (read / write), the list of documents it can read, and unread channel messages that mention it plus unread counts of the channels it is in (mentions). With include_bodies true the full document bodies are attached (up to 20 documents; the rest are listed only). Document permissions follow the agent's docAccess.", true,
			obj(map[string]any{
				"agent":          pStr("Agent id or name"),
				"include_bodies": pBool("Attach the full document bodies, default false"),
			}, "agent")),
		coreDef("agent_tasks",
			"With id, returns the full task; otherwise lists tasks (open ones by default: queued, in progress, blocked).", true,
			obj(map[string]any{
				"agent":  pStr("List only this agent's tasks"),
				"id":     pStr("Task id, e.g. T-3"),
				"status": pEnum("open (default), all, or a single status", "open", "all", "queued", "inProgress", "blocked", "done", "canceled"),
			})),
		coreDef("agent_task_claim",
			"Claims a task as an agent (status becomes in progress). With id, claims that task; without id, claims the agent's highest-priority queued task. Returns the agent's instructions, the task description and an agent credential bound to this task (credential; the secret is shown only this once); use it to connect to the agent endpoint and work as that agent. When there is no task, task is null.", false,
			obj(map[string]any{
				"agent": pStr("Agent id or name"),
				"id":    pStr("Task id (optional)"),
			}, "agent")),
		coreDef("agent_task_report",
			"Reports task progress or result. note is a progress log; result is the final result (Markdown); set status=done when finished, or blocked when a human decision is needed and explain it in note. After done or blocked the agent credential is revoked immediately.", false,
			obj(map[string]any{
				"id":     pStr("Task id"),
				"links":  pStrs("Related issue ids or URLs"),
				"note":   pStr("Progress log"),
				"result": pStr("Result (Markdown)"),
				"status": pEnum("New status", "inProgress", "blocked", "done"),
			}, "id")),
		coreDef("agent_upsert",
			"Creates an agent, or modifies an existing one (give id or name). Give only the fields to change. routines replaces all routines (tasks are queued for the agent automatically when due); add_routines appends. Routine format: {title, instructions, kind: daily|weekly|hourly, hour, minute, weekday(1=Sunday...7=Saturday), every_hours, enabled}.", false,
			obj(map[string]any{
				"active":       pBool("Enabled"),
				"add_routines": pObjs("Routines to add"),
				"color":        pEnum("Color", "blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"),
				"doc_access": map[string]any{"type": "object", "description": "Cabinet access scope, merged into the existing setting: the key is a section id or name, or a tab URL, name or code (stored as a section id or tab URL); the value is read, write or none (remove). A tab's setting takes precedence over its section's." +
					"A key without a prefix applies to the no-project cabinet only; write a project location as \"<project id>:<section or tab>\" (e.g. PRJ-1:docs, PRJ-1:/docs/1), resolved against that project's structure.",
					"additionalProperties": map[string]any{"type": "string", "enum": []string{"none", "read", "write"}}},
				"id":           pStr("Id or name of the agent to modify; when creating, you may give an English id (e.g. product-manager), otherwise one is generated"),
				"instructions": pStr("Agent instructions (the description for the AI, Markdown)"),
				"name":         pStr("Name (required when creating)"),
				"role":         pStr("One-sentence role"),
				"routines":     pObjs("Replaces all routines"),
				"symbol":       pStr("Icon name"),
			})),
		coreDef("agent_task_create",
			"Creates a task assigned to an agent, waiting to be claimed (organization admin). assign_to is the agent that takes the task. "+
				"Give issue to link the task to an issue (the same link tasks created by assigning an issue carry; the issue's page then shows the task and its need-help state); title and instructions are used as given. Unknown or invisible issue: error issueNotFound, no task created.", false,
			obj(map[string]any{
				"assign_to":    pStr("Id or name of the agent that takes this task"),
				"instructions": pStr("Task description (Markdown)"),
				"issue":        pStr("Optional: the issue this task is about, a key (e.g. ISS-3, APP-12) or an internal id; one issue only"),
				"priority":     pEnum("Priority, default normal", prio...),
				"title":        pStr("Title"),
			}, "assign_to", "title")),
		coreDef("agent_task_transfer",
			"Transfers one agent's tasks to another agent without deleting the agent (organization admin). To delete an agent that has tasks, transfer them with this first (or name the recipient at deletion time). "+
				"scope=open (default) transfers only queued and blocked tasks; scope=all transfers everything: tasks in progress (already claimed) are first returned to queued with the lock and credential revoked, and done and canceled tasks are transferred too. Each task gets a note \"transferred from <old agent>\". The target agent must not be the source itself, must exist and must not be disabled.", false,
			obj(map[string]any{
				"from":  pStr("Source agent id or name"),
				"scope": pEnum("Scope, default open", "open", "all"),
				"to":    pStr("Target agent id or name"),
			}, "from", "to")),
		coreDef("projects",
			"Lists the organization's projects (id, name, key, description, linked repos, restricted, myRole, number of documents). A restricted project is visible only to project members and organization admins; ones you cannot see do not appear in the list. With id, shows only that project plus its member list (human endpoint; a project you cannot see returns not found). Projects split the cabinet and issues: each project has its own cabinet structure and documents; a project with a key can hold issues (issue_create and issue_update take project); anything not in a project is \"no project\" / organization level. "+
				"The cabinet tools (cabinet, docs, doc_save, cabinet_layout_save) take project to operate on that project; without it they operate on the no-project cabinet.", true,
			obj(map[string]any{"id": pStr("Show only this project (including its members); omitted = all")})),
		coreDef("project_save",
			"Creates a project (no id; name is required) or modifies a project's name, key or description (with id) (organization admin). The name must be unique within the organization; a new project's id is PRJ-<n> and its cabinet structure is empty (create sections with cabinet_layout_save and project). "+
				"Issues can be created in a project only after its key (e.g. APP) is set, and numbers are <key>-<n>; the key cannot be changed once the project has issues.", false,
			obj(map[string]any{
				"id":             pStr("Id of the project to modify, e.g. PRJ-1; omit when creating"),
				"key":            pStr("Project key: 2 to 10 uppercase letters or digits, starting with a letter, unique within the organization; ISS, PRJ, T, DOC, CH and INC are reserved"),
				"name":           pStr("Project name (required when creating)"),
				"description":    pStr("Project description"),
				"link_repos":     pStrs("Link these source code repositories (owner/name or group/sub/project, a github: or gitlab: prefix is allowed, case-insensitive; already linked ones are skipped). A link is used only for defaults and filtering: when an issue is created without project and its repo is linked to exactly one project, it goes into that project; when issues is filtered by repo, issues without a repo mark inside a project linked to that repo also match. A repo linked to no project = shared by the organization; links are not a permission source and do not move existing issues"),
				"unlink_repos":   pStrs("Unlink these source code repositories (an error is returned for ones that were not linked)"),
				"restricted":     pBool("Restricted project (only organization admins can change it): true = visible only to project members and organization admins (issues, cabinet and the project itself; to everyone else it looks like it does not exist), false = visible to all organization members (default). When changed to restricted, unclaimed tasks for this project's issues held by agents that are not project members are canceled"),
				"add_members":    pStrs("Adds project members (organization admin or project admin): a user (email, user:<email>) or an agent (agent:<agent id>). If already a member, the role is changed. Issues of a restricted project can only be assigned to member agents"),
				"remove_members": pStrs("Removes project members (organization admin or project admin; an error is returned for a non-member). When an agent member is removed, its unclaimed tasks in the restricted project are canceled as well"),
				"member_role":    pEnum("Role for add_members, default member (omitted and already a member = role unchanged)", "member", "admin"),
			})),
		coreDef("cabinet",
			"Lists the cabinet's sections and tabs, with the number of documents in each. With project, shows that project's cabinet (omitted = no project).", true,
			obj(map[string]any{
				"project": pStr(projectParam),
			})),
		coreDef("docs",
			"With id, reads one document (document ids are unique within the organization, so project is not needed; the returned project is the one it belongs to); otherwise lists documents by project, section, tab and keyword (up to 50; only the given project's, omitted = no project). "+
				"To save context when reading long documents, read in parts: first outline to see the structure (each part's id, title, line range and character count), then part to read one part, from/to to read some lines, or grep to find a keyword; "+
				"pass the returned version back when editing partially (edits, replace_part, append_to_part of doc_save). With only id, the full text is returned, but a document over 6000 characters returns only the outline and the beginning (pass full for the whole text).", true,
			obj(map[string]any{
				"project": pStr(projectParam + "; when listing, only this project's documents are listed, and section and tab are resolved against its structure (not needed with id)"),
				"id":      pStr("Document id, e.g. DOC-3"),
				"query":   pStr("Title, body or tags contain this text"),
				"section": pStr("Section id or name"),
				"tab":     pStr("Tab URL, name or code"),
				"outline": pBool("With id, return only the outline and version"),
				"part":    pStr("With id, read only this part: an id from the outline (e.g. s3) or a title"),
				"from":    pInt("With id, the line to start reading from (1-based, inclusive)"),
				"to":      pInt("With id, the line to read up to (inclusive)"),
				"grep":    pStr("With id, return only lines containing this text (with line numbers and surrounding context)"),
				"context": pInt("Lines of context before and after each grep match, default 2"),
				"full":    pBool("With id, always return the full text (even for long documents)"),
			})),
		coreDef("doc_save",
			"Creates a document (no id) or modifies one (with id). The content is plain text/Markdown, placed in the given section and tab. The signature is the caller (on the agent endpoint, the credential's agent, which can write only to locations where the agent has read-write access). "+
				"Projects: when creating, project is the project to put it in (omitted = no project), and section and tab are resolved against that project's structure; when modifying, giving project moves the document to that project (empty string = move back to no project; without section the original section and tab are kept, and the target project must have them), omitted = no move. "+
				"The body may start with fields (one key: value per line between ---); kind is the document type: when a matching template exists (a document with kind: template and for: <type>, containing an ```html layout and ```css styles), the page is rendered by the template, and the template's required fields are also checked before saving. "+
				"When modifying long documents, prefer partial edits (choose one of edits, replace_part, append_to_part; no need to send the full text); the response contains only the changed lines and the new version. Every successful save (create, full update, partial edit) returns the new version, which can be passed as base_version to the next partial edit without re-reading.", false,
			obj(map[string]any{
				"append_body": pStr("Text to append after the body"),
				"body":        pStr("Replaces the body (Markdown)"),
				"edits": map[string]any{"type": "array", "description": "Partial edit: string replacements applied in order; old must occur exactly once in the full text (or set all); if any one fails, nothing is changed",
					"items": obj(map[string]any{"old": pStr("Original text to be replaced (include surrounding context to make it unique)"), "new": pStr("The new text"), "all": pBool("Replace every occurrence")}, "old", "new")},
				"replace_part":   pStr("Partial edit: replaces this whole part (outline id or title), keeping the heading line; put the content in content"),
				"append_to_part": pStr("Partial edit: append at the end of this part (outline id or title); put the content in content; if it carries its own heading, a new section is inserted after this part"),
				"content":        pStr("Content for replace_part and append_to_part (Markdown)"),
				"base_version":   pStr("For partial edits, pass the version you read: when the version does not match, edits is applied again on the latest body, while replace_part and append_to_part are rejected"),
				"id":             pStr("Id of the document to modify; omit when creating"),
				"project":        pStr(projectParam + "; when creating, the project to put it in; when modifying, giving it moves the document to this project (empty string = no project)"),
				"section":        pStr("Section id or name (required when creating, or give only tab)"),
				"tab":            pStr("Tab URL or name"),
				"tags":           pStrs("Tags"),
				"title":          pStr("Title (required when creating)"),
			})),
		coreDef("cabinet_layout_save",
			"Adds, renames and removes the cabinet's sections and tabs (organization admin). With project, changes that project's structure (each project has its own; omitted = no project). Applied in order add_sections → add_tabs → update → remove; saved only if all succeed (one save). "+
				"A new section without id gets s<n> automatically; a new tab without path gets /<section id>/<n>. Removed sections and tabs must not still contain documents (move them away with doc_save first). Returns the new structure, the ids added by this call and the new layoutVersion. "+
				"Optimistic locking: cabinet returns layoutVersion; when you pass expectedVersion (the one read from cabinet) and the structure was changed by someone else in between (version mismatch), the call is rejected and returns the current version, so re-read with cabinet and redo; without it, the version read at the start of this call is used.", false,
			obj(map[string]any{
				"project":         pStr(projectParam),
				"expectedVersion": pInt("Optional: the layoutVersion that cabinet returned when you read the structure; on a mismatch (the structure was updated by someone else) the call is rejected and nothing is overwritten"),
				"add_sections":    pObjs("Sections to add: [{label, id?}]; appended at the end"),
				"add_tabs":        pObjs("Tabs to add: [{section, label, code?, group?, path?}]; section is a section id or name (may be one added in this call)"),
				"update":          pObjs("Renames: [{section or tab, label?, code?, group?}]; section is a section id or name, tab is a tab URL, name or code (may also carry section to narrow it); code and group set to an empty string are cleared"),
				"remove":          pStrs("Sections (id or name) or tabs (URL, name or code) to remove; a section is removed together with its tabs"),
			})),
	}
	defs = append(defs, ChainToolDefs()...)
	defs = append(defs, ChannelToolDefs()...)
	defs = append(defs, IncidentToolDefs()...)
	defs = append(defs, DecisionToolDefs()...)
	defs = append(defs, ItemToolDefs()...)
	defs = append(defs, CommitToolDefs()...)
	defs = append(defs, WireframeToolDefs()...)
	return append(defs, FlowchartToolDefs()...)
}

// CoreInitialize initialize 的回應內容(protocolVersion 由協定層決定)
func CoreInitialize(instructions string) map[string]any {
	return map[string]any{
		"capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
		"instructions": instructions,
		"serverInfo":   map[string]any{"name": "omixor", "title": "omixor", "version": "0.1.0"},
	}
}

// NewCoreServer 用核心的工具規格建立協定處理器,並登記組織、Issue、虛擬角色工具的實作
func NewCoreServer(instructions string) *Server {
	tj, _ := json.Marshal(CoreToolDefs())
	ij, _ := json.Marshal(CoreInitialize(instructions))
	s, err := NewServer(tj, ij)
	if err != nil {
		panic(err)
	}
	RegisterOrgTools(s)
	RegisterSchemaTools(s)
	RegisterIssueTools(s)
	RegisterAgentTools(s)
	RegisterCabinetTools(s)
	RegisterChainTools(s)
	RegisterChannelTools(s)
	RegisterIncidentTools(s)
	RegisterDecisionTools(s)
	RegisterItemTools(s)
	RegisterCommitTools(s)
	RegisterWireframeTools(s)
	RegisterFlowchartTools(s)
	return s
}

// WorkerToolDefs 執行者端點(WorkerEndpoint)的工具規格:只有派發需要的,前綴 dispatch_
func WorkerToolDefs() []any {
	return []any{
		coreDef("dispatch_queue",
			"Lists the tasks I can claim: queued tasks of all enabled agents, blocked tasks that can be resumed, and the tasks I have claimed (inProgress). "+
				"Sorted by priority and creation time. Each entry has id, title, agent, priority, status and links (related issue ids); locked ones are marked claimable=false with lockedUntil and claimedBy.", true,
			obj(map[string]any{
				"agent": pStr("List only this agent's (id or name) tasks"),
				"limit": pInt("Maximum number of results, default 30, at most 100"),
			})),
		coreDef("dispatch_claim",
			"Claims a task and returns the agent info and credential: with id, claims that task; without id, claims the highest-priority queued task (oldest first within the same priority) among all enabled agents (with agent, limited to that agent). "+
				"Returns task, agent (id, name, role, instructions), credential (agentId, secret, code, endpoint, expiresAt; the secret is shown only this once; code is a 6-digit claim verification code that dispatch_report and dispatch_comment require), lockedUntil and nextSteps (what to do next). "+
				"Claiming locks the task, and nobody else can claim it while locked (a locked task returns an error stating when the lock ends); when no task is claimable, task is null.", false,
			obj(map[string]any{
				"agent": pStr("Agent id or name (optional; limits to that agent)"),
				"id":    pStr("Task id, e.g. T-3 (optional; omitted = the highest-priority one is picked automatically)"),
			})),
		coreDef("dispatch_status",
			"Lists the tasks I am working on (inProgress): lock expiry time, minutes remaining, and whether it has timed out. Timed-out tasks are returned to queued automatically; to renew the lease use dispatch_report (status: inProgress).", true,
			obj(map[string]any{})),
		coreDef("dispatch_report",
			"Reports on a task I am working on (no agent credential needed; the server confirms I am the claimer and reports as that agent): status inProgress (with note to report progress and renew the lease), "+
				"blocked (a human decision is needed; ask in note) or done (write the result in result). After done or blocked the task ends and the credential is revoked. The task must be in progress and the lease not yet expired.", false,
			obj(map[string]any{
				"id":     pStr("Task id, e.g. T-3"),
				"code":   pStr("Claim verification code (credential.code returned by dispatch_claim, 6 digits)"),
				"status": map[string]any{"type": "string", "enum": []string{"inProgress", "blocked", "done"}, "description": "Status (optional; omitted = only add a note or result)"},
				"result": pStr("Result (write it when done)"),
				"note":   pStr("Note (progress, questions), signed by that agent"),
				"links":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Related issue ids (optional, appended)"},
			}, "id", "code")),
		coreDef("dispatch_comment",
			"Comments on an issue, signed as the agent of the task I am working on (agent:<agent id>); no agent credential needed. The task must be one I claimed, in progress, with an unexpired lease; comments can only go on issues in the task's links.", false,
			obj(map[string]any{
				"id":      pStr("Id of my in-progress task (decides which agent signs)"),
				"code":    pStr("Claim verification code (credential.code returned by dispatch_claim, 6 digits)"),
				"issue":   pStr("Issue id, e.g. ISS-13"),
				"comment": pStr("Comment content (Markdown)"),
			}, "id", "code", "issue", "comment")),
		coreDef("dispatch_release",
			"Abandons a task I am working on: returns it to queued, unlocks it, revokes the credential, and leaves a note on the task (signed by the one who abandoned it, with the reason). Only the claimer or an organization admin can do it.", false,
			obj(map[string]any{
				"id":     pStr("Task id"),
				"reason": pStr("Reason for abandoning (optional; written into the note)"),
			}, "id")),
		coreDef("dispatch_delegate",
			"As the agent of the task I am working on, delegates work to a downstream agent on the organization chain (the chain must contain agent:<this agent> → agent:<to>; see nextSteps of dispatch_claim for who you can delegate to). "+
				"The new task is signed by this agent, links include this task and its links, and a label (goal, task...) is written at the start of the task description; when the other side finishes with done or blocked, a \"review report\" task is queued for this agent automatically. No agent credential needed.", false,
			obj(map[string]any{
				"id":           pStr("Id of my in-progress task (decides which agent delegates)"),
				"code":         pStr("Claim verification code (credential.code returned by dispatch_claim, 6 digits)"),
				"to":           pStr("Id or name of the agent that takes the task (must be downstream on the chain)"),
				"title":        pStr("Title"),
				"instructions": pStr("Task description (Markdown)"),
				"priority":     pEnum("Priority, default normal", "urgent", "high", "normal", "low"),
			}, "id", "code", "to", "title")),
	}
}

// WorkerInitialize 執行者端點 initialize 的回應內容
func WorkerInitialize(instructions string) map[string]any {
	m := CoreInitialize(instructions)
	m["serverInfo"] = map[string]any{"name": "omixor-worker", "title": "omixor dispatch", "version": "0.1.0"}
	return m
}

// NewWorkerServer 執行者端點的協定處理器:只有 dispatch_ 工具
func NewWorkerServer(instructions string) *Server {
	tj, _ := json.Marshal(WorkerToolDefs())
	ij, _ := json.Marshal(WorkerInitialize(instructions))
	s, err := NewServer(tj, ij)
	if err != nil {
		panic(err)
	}
	RegisterDispatchTools(s)
	return s
}
