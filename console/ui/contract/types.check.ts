// 跨語言契約(編譯期):core/model/testdata/contract/*.json(core API 實際回傳的形狀,由 core/model/contract_test.go 產生)
// 要符合前端用的型別。由 tsc -p contract 檢查(npm test),不會被 vite 打包。
//
//   conforms<T>()(json)       範例檔可以指定給 T(字串的列舉放寬成 string,因為 JSON 匯入的型別是 string):欄位少了、型別不同會報錯
//   NoExtra<Extra<J, T>>      範例檔有、T 沒宣告的欄位(Go 改名或新增了):報錯訊息會列出欄位名;前端刻意不用的欄位寫在 Allow
//   NoMissing<Missing<J, T>>  T 宣告了(含選填)、範例檔沒有的欄位(範例已把選填欄位填滿,所以沒有 = Go 那邊改名或拿掉了)
//
// Go 改了 model → 重生範例檔 → 這裡編不過:改前端型別(或把刻意不用的欄位加進 Allow)。
import type { Issue, IssueComment } from "../src/sections/development/DevIssuesTab";
import type {
  Agent, AgentRoutine, AgentTask, CabinetDoc, CabinetSection, CabinetTab, Channel, ChannelMessage, ChannelPage, ChainEdge,
  Incident, IncidentEvent, OrgChain, Organization, Project,
} from "../src/components/models";
import type { Actor } from "../src/types";

import issue from "../../../core/model/testdata/contract/issue.json";
import issueList from "../../../core/model/testdata/contract/issue_list.json";
import agent from "../../../core/model/testdata/contract/agent.json";
import agentTask from "../../../core/model/testdata/contract/agent_task.json";
import agentTaskList from "../../../core/model/testdata/contract/agent_task_list.json";
import channel from "../../../core/model/testdata/contract/channel.json";
import channelList from "../../../core/model/testdata/contract/channel_list.json";
import channelMessage from "../../../core/model/testdata/contract/channel_message.json";
import channelMessages from "../../../core/model/testdata/contract/channel_messages.json";
import incident from "../../../core/model/testdata/contract/incident.json";
import incidentList from "../../../core/model/testdata/contract/incident_list.json";
import cabinet from "../../../core/model/testdata/contract/cabinet.json";
import cabinetDoc from "../../../core/model/testdata/contract/cabinet_doc.json";
import cabinetProject from "../../../core/model/testdata/contract/cabinet_project.json";
import cabinetDocProject from "../../../core/model/testdata/contract/cabinet_doc_project.json";
import project from "../../../core/model/testdata/contract/project.json";
import projectList from "../../../core/model/testdata/contract/project_list.json";
import orgChain from "../../../core/model/testdata/contract/org_chain.json";
import org from "../../../core/model/testdata/contract/org.json";

/** 字串、數字的字面型別放寬(JSON 匯入的型別只有 string/number/boolean) */
type Widen<T> = T extends string ? string
  : T extends number ? number
  : T extends boolean ? boolean
  : T extends readonly (infer U)[] ? Widen<U>[]
  : T extends object ? { [K in keyof T]: Widen<T[K]> }
  : T;

const conforms = <T,>() => <J extends Widen<T>>(j: J): J => j;

type KeysOf<T> = T extends unknown ? keyof T : never;
type Elem<T> = T extends readonly (infer U)[] ? U : T;
/** 範例檔有、T 沒宣告的欄位(扣掉 Allow) */
type Extra<J, T, Allow extends PropertyKey = never> = Exclude<KeysOf<Elem<J>>, keyof T | Allow>;
/** T 宣告了、範例檔沒有的欄位(扣掉 Allow) */
type Missing<J, T, Allow extends PropertyKey = never> = Exclude<keyof T, KeysOf<Elem<J>> | Allow>;
type NoExtra<K extends never> = K;
type NoMissing<K extends never> = K;

// ---- Issue(開發 › Issue 的 Issue)
conforms<Issue>()(issue);
issueList.forEach((i) => conforms<Issue>()(i));
type _issue = [NoExtra<Extra<typeof issue, Issue>>, NoMissing<Missing<typeof issue, Issue>>];
type _issueComment = [NoExtra<Extra<typeof issue.comments, IssueComment>>, NoMissing<Missing<typeof issue.comments, IssueComment>>];

// ---- 虛擬角色與任務
conforms<Agent>()(agent);
// agent_list.json 不在這裡檢查:陣列裡 docAccess 一個有值一個是 {},TS 推出的型別會把 key 變成 ?: undefined,跟 Record 對不上(執行期的測試有讀)
type _agent = [NoExtra<Extra<typeof agent, Agent>>, NoMissing<Missing<typeof agent, Agent>>];
type _routine = [NoExtra<Extra<typeof agent.routines, AgentRoutine>>, NoMissing<Missing<typeof agent.routines, AgentRoutine>>];

// AgentTask:completedAt、claimedAt 前端沒用(只顯示 lockedUntil、claimedBy)
conforms<AgentTask>()(agentTask);
agentTaskList.forEach((t) => conforms<AgentTask>()(t));
type _task = [NoExtra<Extra<typeof agentTask, AgentTask, "completedAt" | "claimedAt">>, NoMissing<Missing<typeof agentTask, AgentTask>>];
type _taskNote = [NoExtra<Extra<typeof agentTask.notes, AgentTask["notes"][number]>>, NoMissing<Missing<typeof agentTask.notes, AgentTask["notes"][number]>>];

// ---- 頻道(Channel 是 core 的 ChannelSummary:頻道加 unread、lastMessageAt)
conforms<Channel>()(channel);
channelList.forEach((c) => conforms<Channel>()(c));
type _channel = [NoExtra<Extra<typeof channel, Channel>>, NoMissing<Missing<typeof channel, Channel>>];
conforms<ChannelMessage>()(channelMessage);
conforms<ChannelPage>()(channelMessages);
type _message = [NoExtra<Extra<typeof channelMessage, ChannelMessage>>, NoMissing<Missing<typeof channelMessage, ChannelMessage>>];
type _page = [NoExtra<Extra<typeof channelMessages, ChannelPage>>, NoMissing<Missing<typeof channelMessages, ChannelPage>>];

// ---- 事件:alertIds、createdBy 前端還沒用
conforms<Incident>()(incident);
incidentList.forEach((i) => conforms<Incident>()(i));
type _incident = [NoExtra<Extra<typeof incident, Incident, "alertIds" | "createdBy">>, NoMissing<Missing<typeof incident, Incident>>];
type _incidentEvent = [NoExtra<Extra<typeof incident.timeline, IncidentEvent>>, NoMissing<Missing<typeof incident.timeline, IncidentEvent>>];
type _actor = [NoExtra<Extra<(typeof incident.timeline)[number]["actor"], Actor>>];

// ---- 文件櫃(CabinetView 是 core 的 Cabinet;layoutSaved 前端沒用)
type CabinetViewLike = { layout: CabinetSection[]; docs: CabinetDoc[] };
conforms<CabinetViewLike>()(cabinet);
type _cabinet = [NoExtra<Extra<typeof cabinet, CabinetViewLike, "layoutSaved">>, NoMissing<Missing<typeof cabinet, CabinetViewLike>>];
type _section = [NoExtra<Extra<typeof cabinet.layout, CabinetSection>>, NoMissing<Missing<typeof cabinet.layout, CabinetSection>>];
type _tab = [NoExtra<Extra<(typeof cabinet.layout)[number]["tabs"], CabinetTab>>, NoMissing<Missing<(typeof cabinet.layout)[1]["tabs"], CabinetTab>>];
conforms<CabinetDoc>()(cabinetDoc);
// 未分專案的文件不帶 project
type _doc = [NoExtra<Extra<typeof cabinetDoc, CabinetDoc>>, NoMissing<Missing<typeof cabinetDoc, CabinetDoc, "project">>];
conforms<CabinetDoc>()(cabinetDocProject);
type _docProject = [NoExtra<Extra<typeof cabinetDocProject, CabinetDoc>>, NoMissing<Missing<typeof cabinetDocProject, CabinetDoc>>];

// ---- 專案(專案的文件櫃多帶 project;layoutSaved 前端沒用)
conforms<Project>()(project);
projectList.forEach((p) => conforms<Project>()(p));
type _project = [NoExtra<Extra<typeof project, Project>>, NoMissing<Missing<typeof project, Project>>];
type CabinetProjectLike = CabinetViewLike & { project: string };
conforms<CabinetProjectLike>()(cabinetProject);
type _cabinetProject = [NoExtra<Extra<typeof cabinetProject, CabinetProjectLike, "layoutSaved">>, NoMissing<Missing<typeof cabinetProject, CabinetProjectLike>>];

// ---- 組織鏈
conforms<OrgChain>()(orgChain);
type _chain = [NoExtra<Extra<typeof orgChain, OrgChain>>, NoMissing<Missing<typeof orgChain, OrgChain>>];
type _edge = [NoExtra<Extra<typeof orgChain.edges, ChainEdge>>, NoMissing<Missing<typeof orgChain.edges, ChainEdge>>];

// ---- 組織(畫面的 Organization 由 web 後端的 orgs view 給;core 的 myRole 前端另外判斷)
conforms<Organization>()(org);
type _org = [NoExtra<Extra<typeof org, Organization, "myRole">>, NoMissing<Missing<typeof org, Organization>>];

export {};
