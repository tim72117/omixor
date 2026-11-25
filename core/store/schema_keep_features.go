package store

// 步驟 4:功能 pipelines.voice(旁白發佈)、monitoring.visitors(網站訪客)的組織層預設值由「開」改成「關」。
// 組織的功能開關只存改過的值(omixor_org_system.features),沒改過的組織是靠預設值開著的,
// 所以升級時幫「當下已經存在的組織」把沒存值的這兩個 key 明確寫成 true;已經存過的值(含 false)不動。
// 不改結構,只寫資料。全新資料庫只記版本、不執行(見 createFreshSchema),之後新建的組織用新的預設值(關)。
// 系統層(omixor_server_features)的預設值沒有變,不用處理。冪等:右邊(原本存的值)蓋過左邊。
const keepVoiceVisitorsSQL = `
insert into omixor_org_system as s (org, features, updated_at)
select id, '{"pipelines.voice": true, "monitoring.visitors": true}'::jsonb, date_trunc('second', now()) from omixor_orgs
on conflict (org) do update set features = excluded.features || s.features
`
