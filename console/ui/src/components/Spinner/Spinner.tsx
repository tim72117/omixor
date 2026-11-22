import s from "./Spinner.module.css";

export function Spinner({ size = 14 }: { size?: number }) {
  return <span className={s.spinner} style={{ width: size, height: size }} />;
}
