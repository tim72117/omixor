// 前端資料模型的唯一入口:domain.ts(監控、事件、AI 自動化、文件櫃、設定、Issue)+ ops.ts(總覽、開發、CI/CD、伺服器)。
// 兩邊原本同名的型別(Service、Server、Deployment、WorkItem、Agent、Automation、AutomationRun、Incident、ActivityItem、
// Alert、MetricPoint、OnCallShift、Person)已合併成一份(取欄位較完整的版本)。
export * from "./domain";
export * from "./ops";
