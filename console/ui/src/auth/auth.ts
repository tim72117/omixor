// 網頁版登入(LDAP)。後端見 internal/web/auth.go。
import { useEffect, useState } from "react";
import { parseAuthResponse } from "./loginError";

export type AuthUser = { id: string; name: string; email?: string; dn?: string; groups?: string[]; admin: boolean };
// noAdmins:啟用 LDAP 但管理員帳號名單是空的(沒有人是管理員)
// denied:有 session 但不能用的原因;repair:管理員對不上使用者,只能改認證設定;unavailable:暫時查不出能不能用(仍是登入中)
export type AuthStatus = { mode: "off" | "ldap"; loggedIn: boolean; user?: AuthUser; noAdmins?: boolean; denied?: string; repair?: boolean; unavailable?: string };

let current: AuthStatus | undefined;
const subs = new Set<(s: AuthStatus) => void>();

// 這個分頁看過的登入者
let seenUser = "";

const loginSubs = new Set<(loggedIn: boolean) => void>();

/** 登入狀態從登入變成登出、或登出變成登入時通知(第一次問到的狀態不算) */
export function onLoginChange(f: (loggedIn: boolean) => void): () => void {
  loginSubs.add(f);
  return () => void loginSubs.delete(f);
}

export async function refreshAuth(): Promise<AuthStatus> {
  const r = await fetch("/api/auth/web/status");
  const next = (await r.json()) as AuthStatus;
  const id = next.loggedIn ? next.user?.id ?? "" : "";
  // 同一個分頁換了人登入:整頁重新載入,不能沿用上一個人的快取(view 快取、他選的組織)
  if (id && seenUser && id !== seenUser) {
    window.location.reload();
    return next;
  }
  if (id) seenUser = id;
  const was = current?.loggedIn;
  current = next;
  subs.forEach((f) => f(current!));
  if (was !== undefined && was !== next.loggedIn) loginSubs.forEach((f) => f(next.loggedIn));
  return current;
}

export function useAuth(): AuthStatus | undefined {
  const [s, setS] = useState(current);
  useEffect(() => {
    subs.add(setS);
    if (!current) refreshAuth().catch(() => {});
    return () => void subs.delete(setS);
  }, []);
  return s;
}

/** 目前的網頁使用者是不是 web 管理員(沒啟用 LDAP 時一律是) */
export function useIsWebAdmin() {
  const auth = useAuth();
  return auth?.mode !== "ldap" || (auth.loggedIn && !!auth.user?.admin);
}

async function post(path: string, body: unknown) {
  const r = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json", "X-Omixor-CSRF": "1" }, body: JSON.stringify(body) });
  return parseAuthResponse(r.status, r.ok, r.headers.get("Retry-After"), await r.text());
}

export async function login(username: string, password: string) {
  await post("/api/auth/web/login", { username, password });
  await refreshAuth();
}

export async function logout() {
  await post("/api/auth/web/logout", {});
  await refreshAuth();
}

export type LdapConfig = {
  mode: "off" | "ldap";
  sessionHours: number;
  admins: string[];
  emailDomain: string;
  trustMail: boolean;
  // 被主機環境變數蓋掉的項目(只有讀取時有)
  env?: { admins?: string[]; emailDomain?: string; trustMail?: boolean };
  ldap: {
    url: string; startTLS: boolean; caCert: string; insecureSkipVerify: boolean;
    userDNTemplate: string; bindDN: string; hasBindPassword: boolean; baseDN: string; userFilter: string;
    idAttr: string; nameAttr: string; mailAttr: string;
    requiredGroup: string; groupFilter: string; groupBaseDN: string;
  };
};

export async function getConfig(): Promise<LdapConfig> {
  const r = await fetch("/api/auth/config");
  const out = await r.json();
  if (!r.ok) throw new Error(out?.error ?? `HTTP ${r.status}`);
  return out as LdapConfig;
}

export const testConfig = (config: LdapConfig, bindPassword: string | undefined, username: string, password: string) =>
  post("/api/auth/test", { config, bindPassword, username, password }) as Promise<AuthUser>;

export const saveConfig = (config: LdapConfig, bindPassword: string | undefined, testUsername: string, testPassword: string) =>
  post("/api/auth/config", { config, bindPassword, testUsername, testPassword }) as Promise<LdapConfig>;
