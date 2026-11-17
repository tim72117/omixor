// 網頁版登入(LDAP)。後端見 internal/web/auth.go。
import { useEffect, useState } from "react";

export type AuthUser = { id: string; name: string; email?: string; dn?: string; groups?: string[]; admin: boolean };
export type AuthStatus = { mode: "off" | "ldap"; loggedIn: boolean; user?: AuthUser };

let current: AuthStatus | undefined;
const subs = new Set<(s: AuthStatus) => void>();

export async function refreshAuth(): Promise<AuthStatus> {
  const r = await fetch("/api/auth/status");
  current = (await r.json()) as AuthStatus;
  subs.forEach((f) => f(current!));
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

async function post(path: string, body: unknown) {
  const r = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const out = await r.json();
  if (out?.error) throw new Error(out.error);
  return out.result ?? out;
}

export async function login(username: string, password: string) {
  await post("/api/auth/login", { username, password });
  await refreshAuth();
}

export async function logout() {
  await post("/api/auth/logout", {});
  await refreshAuth();
}

export type LdapConfig = {
  mode: "off" | "ldap";
  sessionHours: number;
  ldap: {
    url: string; startTLS: boolean; caCert: string; insecureSkipVerify: boolean;
    userDNTemplate: string; bindDN: string; hasBindPassword: boolean; baseDN: string; userFilter: string;
    idAttr: string; nameAttr: string; mailAttr: string;
    requiredGroup: string; adminGroup: string; groupFilter: string; groupBaseDN: string;
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
