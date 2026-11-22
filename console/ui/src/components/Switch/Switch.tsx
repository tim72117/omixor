import s from "./Switch.module.css";

/** 開關;點擊不往上傳 */
export function Switch({ on, onChange, disabled, title, small }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; title?: string; small?: boolean }) {
  return (
    <button
      className={s.switch + (on ? " " + s.on : "") + (small ? " " + s.small : "")}
      disabled={disabled}
      title={title}
      onClick={(e) => { e.stopPropagation(); onChange(!on); }}
      role="switch"
      aria-checked={on}
    >
      <span />
    </button>
  );
}
