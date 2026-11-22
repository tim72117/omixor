// 伺服器與服務:服務、伺服器、服務地圖(範例資料)與 GCP(真實 Cloud Run)。Swift 的 InfrastructureView.swift。
import { useEffect, useState } from "react";
import { Segmented } from "../../components/Segmented";
import { SIcon } from "../../components/SIcon";
import { OmixorProvider, useOmixor } from "../../lib/directory";
import { useList } from "../../lib/hooks";
import type { Server } from "../../models";
import type { SectionProps } from "..";
import { InfraServicesTab } from "./InfraServicesTab";
import { InfraServersTab } from "./InfraServersTab";
import { InfraMapTab } from "./InfraMapTab";
import { InfraGCPTab } from "./InfraGCPTab";
import { useFeatureTabs } from "../../features";
import css from "./infrastructure.module.css";
import { layoutClass } from "../../components/Layout";

type Tab = "services" | "servers" | "map" | "gcp";
const TABS: { value: Tab; label: string }[] = [{ value: "services", label: "服務" }, { value: "servers", label: "伺服器" }, { value: "map", label: "服務地圖" }, { value: "gcp", label: "GCP" }];

export default function InfrastructureView(props: SectionProps) {
  return <OmixorProvider><Infrastructure {...props} /></OmixorProvider>;
}

function Infrastructure({ focusId, clearFocus }: SectionProps) {
  const o = useOmixor();
  const [servers, , serversReady] = useList<Server>("servers");
  const [rawTab, setRawTab] = useState<Tab>("services");
  const tabs = useFeatureTabs(TABS.map((x) => x.value), rawTab, setRawTab, (x) => `infrastructure.${x}`);
  const tab = tabs.shown, setTab = tabs.select;
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
    <div className={layoutClass.page}>
      <div className={"row " + css.header}>
        <div className={"col gap4 " + css.titleCol}>
          <div className="page-title">伺服器與服務</div>
          <div className={"row muted ellipsis " + css.gap10}>
            <span className="row gap4"><SIcon name="square.stack.3d.up" size={12} />{o.services.length} 個服務</span>
            <span className="row gap4"><SIcon name="server.rack" size={12} />{servers.length} 台主機</span>
            {badSvc + badSrv > 0 ? <span className={"row gap4 " + css.warn}><SIcon name="exclamationmark.triangle.fill" size={12} color="orange" />{badSvc} 個服務、{badSrv} 台主機需要注意</span>
              : o.services.length > 0 && <span className={"row gap4 " + css.ok}><SIcon name="checkmark.circle.fill" size={12} color="green" />全部正常</span>}
          </div>
        </div>
        <span className="grow" />
        <div className={css.tabs}>
          <Segmented value={tab} onChange={setTab} options={TABS.filter((x) => tabs.on(x.value))} />
        </div>
      </div>
      <div className={layoutClass.page}>
        {tab === "services" && <InfraServicesTab selection={selectedService} setSelection={setSelectedService} servers={servers} onOpenServer={openServer} />}
        {tab === "servers" && <InfraServersTab selection={selectedServer} setSelection={setSelectedServer} servers={servers} onOpenService={openService} />}
        {tab === "map" && <InfraMapTab selection={selectedService} setSelection={setSelectedService} onOpenService={openService} />}
        {tab === "gcp" && <InfraGCPTab />}
      </div>
    </div>
  );
}
