// 名稱目錄與頁面共用資料(合併原本 bits.tsx 的 DirectoryProvider 與 omixorKit.tsx 的 OmixorProvider)。
//
//   DirectoryProvider(App 最外層一次):讀 people、automations、agents、me,提供 useDirectory()。
//   OmixorProvider(總覽、開發、CI/CD、伺服器頁各包一次):另外讀 services、repos、nav,
//     人名等直接沿用 DirectoryProvider 的資料(不再重複讀 people/automations/agents/me),提供 useOmixor()。
//
// 名稱的後備規則只有一份(actorName):沒有 actor →「未指派」、人查不到 → id(沒有 id 是「?」)、
// AI → 自動化名稱 / 角色名稱 / 「MCP 用戶端」/「AI」、其他 →「系統」。
// me 是目前登入者的 id;讀到前是空字串(ready 為 false),不再假設成 "tim"。
import { createContext, useContext, useMemo, type ReactNode } from "react";
import { useView } from "../api";
import { useFeatures } from "../features";
import { guardedNavigate } from "./nav";
import type { Agent, Automation, Person, Repository, Service } from "../models";
import type { Actor, Nav, SectionId } from "../types";

export type Directory = {
  /** 目前登入者的 person id;還沒讀到是 "" */
  me: string;
  /** me 已經讀到 */
  ready: boolean;
  /** me 能當指派對象、指揮官、審查者:是人員清單裡的人,或是登入者本人(後端接受本人) */
  assignable: string;
  people: Person[];
  automations: Automation[];
  agents: Agent[];
  person: (id?: string) => Person | undefined;
  agent: (id?: string) => Agent | undefined;
  name: (a?: Actor | null) => string;
  /** Issue/文件的作者字串:person id、"mcp"、"routine" 或 "agent:<id>" */
  authorName: (author: string) => string;
};

const arr = <T,>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);

type Me = { id: string; name?: string; signedIn?: boolean };

function buildDirectory(self: Me | undefined, people: Person[], automations: Automation[], agents: Agent[]): Directory {
  const me = self?.id ?? "";
  const ready = !!me;
  const person = (id?: string) => people.find((p) => p.id === id);
  const agent = (id?: string) => agents.find((a) => a.id === id);
  // 登入者不在人員清單時用登入的顯示名稱
  const selfName = (id?: string) => (self?.signedIn && id === me ? self.name : undefined);
  const name = (a?: Actor | null) => {
    if (!a) return "未指派";
    if (a.kind === "person") return person(a.id)?.name ?? selfName(a.id) ?? a.id ?? "?";
    if (a.kind === "ai") {
      return automations.find((x) => x.id === a.id)?.name ?? agent(a.id)?.name ?? (a.id === "mcp" ? "MCP 用戶端" : "AI");
    }
    return "系統";
  };
  const authorName = (author: string) => {
    if (author === "routine") return "例行排程";
    if (author.startsWith("agent:")) return agent(author.slice(6))?.name ?? author;
    if (author === "mcp") return "MCP 用戶端";
    return person(author)?.name ?? selfName(author) ?? author;
  };
  const assignable = self?.signedIn || person(me) ? me : "";
  return { me, ready, assignable, people, automations, agents, person, agent, name, authorName };
}

const EMPTY = buildDirectory(undefined, [], [], []);
const DirCtx = createContext<Directory | null>(null);

/** 放在 App 最外層,讓頭像與名稱共用一份資料 */
export function DirectoryProvider({ children }: { children: ReactNode }) {
  const [people] = useView<Person[]>("people");
  const [autos] = useView<Automation[]>("automations");
  const [agents] = useView<{ agents: Agent[] }>("agents");
  const [me] = useView<Me>("me", ["orgs"]);
  const dir = useMemo(
    () => buildDirectory(me, arr<Person>(people), arr<Automation>(autos), arr<Agent>(agents?.agents)),
    [me, people, autos, agents],
  );
  return <DirCtx.Provider value={dir}>{children}</DirCtx.Provider>;
}

/** 名稱目錄;不在 DirectoryProvider 裡時回傳空目錄(名稱用後備規則) */
export function useDirectory(): Directory {
  return useContext(DirCtx) ?? EMPTY;
}

// MARK: - 頁面共用資料(服務、倉庫、導覽)

export type Shared = Directory & {
  services: Service[];
  repos: Repository[];
  nav?: Nav;
  service: (id: string) => Service | undefined;
  go: (section: SectionId, focusId?: string | null) => void;
  /** 能不能跳到這個頁面(系統設定關掉的頁面,連結與按鈕不顯示) */
  canGo: (section: SectionId) => boolean;
};
const SharedCtx = createContext<Shared | null>(null);

/** 頁面最外層包一次,子元件用 useOmixor() 取人名、服務、導覽;要放在 DirectoryProvider 裡 */
export function OmixorProvider({ children }: { children: ReactNode }) {
  const dir = useDirectory();
  const [services] = useView<Service[]>("services");
  const [repos] = useView<Repository[]>("repos");
  const [nav] = useView<Nav>("nav");
  const flags = useFeatures();
  const svcs = arr<Service>(services);
  const value: Shared = {
    ...dir,
    services: svcs,
    repos: arr<Repository>(repos),
    nav,
    service: (id) => svcs.find((s) => s.id === id),
    go: (section, focusId) => { guardedNavigate(section, focusId).catch(() => undefined); },
    canGo: flags.visible,
  };
  return <SharedCtx.Provider value={value}>{children}</SharedCtx.Provider>;
}

export function useOmixor(): Shared {
  const v = useContext(SharedCtx);
  if (!v) throw new Error("useOmixor 要放在 OmixorProvider 裡");
  return v;
}
