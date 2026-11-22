// 確認框(confirmationDialog;原本在 omixorKit.tsx)
import { useState, type ReactNode } from "react";
import { Modal } from "../Modal";

export type ConfirmSpec = { title: string; message?: ReactNode; buttons: { label: string; onClick: () => void; danger?: boolean; primary?: boolean }[] };

/** const [dlg, ask] = useConfirm(); ask({...}); 把 dlg 放進畫面 */
export function useConfirm(): [ReactNode, (s: ConfirmSpec) => void] {
  const [spec, setSpec] = useState<ConfirmSpec | null>(null);
  const el = (
    <Modal open={!!spec} onClose={() => setSpec(null)} title={spec?.title} width={440}>
      {spec?.message && <div className="muted" style={{ whiteSpace: "pre-wrap", lineHeight: 1.5 }}>{spec.message}</div>}
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={() => setSpec(null)}>取消</button>
        {spec?.buttons.map((b) => (
          <button key={b.label} className={"btn" + (b.primary ? " primary" : "") + (b.danger ? " danger" : "")} onClick={() => { setSpec(null); b.onClick(); }}>{b.label}</button>
        ))}
      </div>
    </Modal>
  );
  return [el, setSpec];
}
