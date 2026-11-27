// view / action 查詢的快取 key 與讀取方式(api.ts 用;測試在 contract/viewStore.test.ts)。
// 規則:load 只能由 key 決定。viewStore 建立一筆時記下 load、之後重讀都用它,如果 load 讀的是「元件目前的參數」,
// 切到專案 A 之後,「未分專案」那一筆重讀時會拿到 A 的資料(受限專案的內容跑到別的 key 底下)。

export type QueryResult = { data: unknown; version?: number };
export type StoreQuery = { key: string; load: () => Promise<QueryResult>; ownTopics: string[] };

/** read 只能用到 name */
export function viewQuery(name: string, read: () => Promise<QueryResult>): StoreQuery {
  return { key: `view:${name}`, load: read, ownTopics: [name] };
}

/** 參數在這裡就序列化定下來:load 送出的永遠是 key 裡的那一組,之後呼叫端改了 args 物件也不影響 */
export function actionQuery(name: string, args: Record<string, unknown>, post: (path: string, body: unknown) => Promise<QueryResult>): StoreQuery {
  const argsKey = JSON.stringify(args);
  return { key: `action:${name}:${argsKey}`, load: () => post(`/api/action/${name}`, JSON.parse(argsKey)), ownTopics: [] };
}
