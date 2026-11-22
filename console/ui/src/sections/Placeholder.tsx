import { EmptyState } from "../components/EmptyState";

export function Placeholder({ title }: { title: string }) {
  return <EmptyState title={title} icon="hammer" message="這一頁還在搬到 Go + React 版。" />;
}
