import type { Actor } from "../types";

export const isAI = (a?: Actor | null) => a?.kind === "ai";
