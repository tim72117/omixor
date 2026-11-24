# MCP vocabulary

All text that the MCP server shows to an LLM (tool descriptions, parameter descriptions, server `instructions`, and later also error and hint messages) is written in English. Use exactly one English term per concept so descriptions, instructions and messages stay consistent. Tool names, parameter names, ids and enum values are never translated.

| English term | Chinese UI term | Where it is used |
| --- | --- | --- |
| organization (parameter and id: `org`) | 組織 | `orgs`, the `org` parameter of the unified entry point, `/mcp/org/<id>`; ids are organization ids |
| issue | Issue | `issues`, `issue_create`, `issue_update`, `issue_accept`; key `ISS-<n>`, or `<project key>-<n>` inside a project |
| issue item (key like `ISS-37.2`) | 子項 | `issue_items`, `issue_item_add`, `issue_item_claim`, `issue_item_report`, `issue_item_update`, `issue_item_promote`; the key is `<issue key>.<sequence>`; "item" is acceptable once the context is clear |
| commit | commit | `commits`, `commits_report`; identified by repository + full 40-character `sha` (show the first 7 as `sha7`); only metadata is stored (message, author, times, branch), never code; `verified=false` means a user reported it and omixor did not check it against the repository |
| commit link | commit 連結 | `issue_commit_link`, `issue_commit_unlink`, and the `commits` / `commits_total` fields of `issues(id)`; one link per issue + commit with a `kind`: `mentions`, `fixes`, `found_in` (the issue was found at this commit), `manual` (any other relation, explained in `note`); links never change an issue's status |
| decision (pending decision) | 待決定 | `issue_decisions`, `issue_decision_add`, `issue_decision_answer`, `issue_decision_to_issue`; id `DEC-<n>`; kind `decision` (a human picks an option) or `help` (a human does something) |
| agent (virtual agent / role) | 虛擬角色 / 角色 | `agents`, `agent_context`, `agent_upsert`, `assignee`, `agent:<id>` nodes; never "role" or "virtual role" for this concept (the agent's `role` field is its one-sentence responsibility) |
| task | 任務 | `agent_tasks`, `agent_task_*`, `dispatch_*`; id `T-<n>`; statuses `queued`, `inProgress`, `blocked`, `done`, `canceled` |
| incident | 事件 | `incidents`, `incident_declare`, `incident_update`; id `INC-<n>` |
| project | 專案 | `projects`, `project_save`, the `project` parameter; id `PRJ-<n>`; "no project" = the unassigned original cabinet / organization level |
| channel | 訊息頻道 | `channels`, `channel_create`, `channel_messages`, `channel_post`; id `CH-<n>`, messages `M-<n>` |
| document (`doc`) | 文件 | `docs`, `doc_save`, the `doc` parameter; id `DOC-<n>` |
| cabinet | 文件櫃 | `cabinet`, `cabinet_layout_save`; made of sections (區塊) and tabs (頁籤); keep the word "cabinet" |
| section / tab | 區塊 / 頁籤 | cabinet structure; a tab is addressed by URL, name or code |
| chain (organization chain) | 組織鏈 | `chain`, `chain_save`, `agent_task_delegate`; edges `from → to` with a label (goal, task) |
| credential | 憑證 | the agent credential returned by `agent_task_claim` / `dispatch_claim` (`agentId:secret`); never "token" for this |
| user token | 使用者 token (`omx_`) | the long-lived personal token, distinct from an agent credential |
| worker | 執行者 | the worker (dispatch) endpoint and `dispatch_*` tools; a session that claims and runs tasks |
| agent endpoint / admin endpoint / unified entry point | 角色端點 / 管理端點 / 統一入口 | `/mcp/agent`, `/mcp/org/<id>`, `/mcp` |
| routine | 例行任務 | a scheduled task template of an agent |
| owner / assignee | 負責人 | the accepter of an issue or item (`assignee` parameter) |
| claim / release | 領取 / 放回 | taking or returning a task or item; "abandon" is only for `dispatch_release` |
| acceptance criteria | 驗收標準 | the `acceptance` parameter of items |

Style rules: imperative or "Returns ..." sentences; keep every limit, default and format; refer to issues by key (`ISS-<n>`), never by internal id, when showing them.

Vocabulary scope by batch (ISS-56): batch 1 = tool and parameter descriptions and server `instructions`; later batches = error and refusal messages, then result hints and messages. Human-facing `Title` fields stay Chinese.

## Tool result shape

Every `tools/call` result (success and failure) is assembled in one place, `core/mcp/result.go` (`OKResult`, `ErrResult`, `FailureResult`), and has the same form:

- `structuredContent` is always a JSON **object**; `content[0].text` is the same object serialized as compact JSON (the MCP back-compat recommendation). Parse either one; they are equal, with one exception for failures (below).
- Success (`isError: false`): the tool's own object is returned as is. A tool that returns a non-object is wrapped as `{"result": <value>}` in both places; an acknowledgement-only tool returns `{}`. The `_meta` keys (`omixor/org`, `omixor/server`, ttl hints) stay in the result's `_meta`.
- Failure (`isError: true`): `{"error": {"code": "<stable machine code>", "message": "<English>", "status": <http-like int>, "data": {...}}}`. `data` is only present when the error carries extra fields (for example `currentVersion` and `current` of a 409 version conflict, `taskCounts` of an agent that still has tasks). Large `error.data` fields (the key `current`, or any value whose compact JSON exceeds 300 bytes) appear only in `structuredContent`; the text's `error.data` then carries only the small fields plus `"truncated": true`, so the model does not see a whole Issue twice. Read `structuredContent` when you need the full data. If `data` cannot be serialized it is dropped and only `code`, `message` and `status` are returned. `code` is the service's own code when it has one (for example `issueNotFound`, `agentHasTasks`, `mcp_admin_required`), otherwise derived from `status`: 400/422 `invalid_argument`, 401 `unauthorized`, 403 `forbidden`, 404 `not_found`, 409 `conflict`, 413 `payload_too_large`, 429 `rate_limited`, 500 `internal`, any other 4xx `request_failed`. Internal errors (database, timeouts) are masked to a generic message with status 500; a plain validation error without a status is 400.
- Lists are always inside a named field of the object (for example `{"issues": [...], "total": n}`), never a bare array.

`core/mcp/contract_test.go` checks this shape for every core tool of the admin endpoint, driven by `model.CoreTools`.
