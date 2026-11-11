// 伺服器與服務:服務、伺服器、服務地圖(範例資料)與 GCP(真實 Cloud Run)。Swift 的 InfrastructureView.swift。
import { useEffect, useState } from "react";
import { OpsProvider, Seg, SIcon, useList, useOps, type Server } from "../../components/opsKit";
import type { SectionProps } from "..";
import { InfraServicesTab } from "./InfraServicesTab";
import { InfraServersTab } from "./InfraServersTab";
import { InfraMapTab } from "./InfraMapTab";
import { InfraGCPTab } from "./InfraGCPTab";

type Tab = "services" | "servers" | "map" | "gcp";

export default function InfrastructureView(props: SectionProps) {
  return <OpsProvider><Infrastructure {...props} /></OpsProvider>;
}

function Infrastructure({ focusId, clearFocus }: SectionProps) {
  const o = useOps();
  const [servers, , serversReady] = useList<Server>("servers");
  const [tab, setTab] = useState<Tab>("services");
  const [selectedService, setSelectedService] = useState<string>();
  const [selectedServer, setSelectedServer] = useState<string>();
  const openServer = (id: string) => { setSelectedServer(id); setTab("servers"); };
  const openService = (id: string) => { setSelectedService(id); setTab("services"); };

  // 其他頁跳過來時選中對應的服務或主機
  useEffect(() => {
    if (!focusId) return;
    if (focusId === "gcp") { setTab("gcp"); clearFocus(); return; }
    if (o.service(focusId)) { openService(focusId); clearFocus(); }
    else if (servers.some((s) => s.id === focusId)) { openServer(focusId); clearFocus(); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, o.services.length, serversReady]);

  const badSvc = o.services.filter((s) => s.health !== "healthy").length;
  const badSrv = servers.filter((s) => s.health !== "healthy").length;
  return (
    <div className="ops-page">
      <div className="row" style={{ padding: "16px 20px 12px", borderBottom: "1px solid var(--line)", gap: 16 }}>
        <div className="col gap4" style={{ minWidth: 0 }}>
          <div className="page-title">伺服器與服務</div>
          <div className="row gap12 muted ellipsis" style={{ gap: 10 }}>
            <span className="row gap4"><SIcon name="square.stack.3d.up" size={12} />{o.services.length} 個服務</span>
            <span className="row gap4"><SIcon name="server.rack" size={12} />{servers.length} 台主機</span>
            {badSvc + badSrv > 0 ? <span className="row gap4" style={{ color: "var(--orange)" }}><SIcon name="exclamationmark.triangle.fill" size={12} color="orange" />{badSvc} 個服務、{badSrv} 台主機需要注意</span>
              : o.services.length > 0 && <span className="row gap4" style={{ color: "var(--green)" }}><SIcon name="checkmark.circle.fill" size={12} color="green" />全部正常</span>}
          </div>
        </div>
        <span className="grow" />
        <div style={{ width: 340, flex: "none" }}>
          <Seg value={tab} onChange={setTab} options={[{ value: "services", label: "服務" }, { value: "servers", label: "伺服器" }, { value: "map", label: "服務地圖" }, { value: "gcp", label: "GCP" }]} />
        </div>
      </div>
      <div className="ops-page">
        {tab === "services" && <InfraServicesTab selection={selectedService} setSelection={setSelectedService} servers={servers} onOpenServer={openServer} />}
        {tab === "servers" && <InfraServersTab selection={selectedServer} setSelection={setSelectedServer} servers={servers} onOpenService={openService} />}
        {tab === "map" && <InfraMapTab selection={selectedService} setSelection={setSelectedService} onOpenService={openService} />}
        {tab === "gcp" && <InfraGCPTab />}
      </div>
    </div>
  );
}
