// view「account」(後端見 internal/core/account.go)
export type Account = {
  localStorage: "postgres" | "files"; error?: string;
  /** 目前操作的使用者 handle(比對 assigneeActor 判斷是不是我);features:支援的功能代號(能力偵測) */
  self?: string; features?: string[];
};
