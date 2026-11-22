import { Pill } from "../Pill";

export function AIBadge({ text = "AI" }: { text?: string }) {
  return <Pill text={text} color="purple" icon="sparkles" />;
}
