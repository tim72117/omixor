package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"omixor-web/internal/office"
)

// 3D 虛擬辦公室(repo 的 office3d/,見 internal/office):
//
//	GET  /office/                 檢視器(office3d/viewer/)與它載入的 glb / json,從磁碟讀(OMIXOR_OFFICE_DIR 或 repo 的 office3d/)
//	POST /api/office/command      下命令:{"text":"工程師 去 會議室"} 或 {"action","agents","target"},可帶 "wait" 秒數
//	GET  /api/office/events       SSE(給檢視器):hello、snapshot、command、role、ping
//	POST /api/office/report       檢視器回報(只採用主檢視器的)
//	GET  /api/office/state        角色最後狀態、檢視器、命令狀態
//
// /api/office/… 和其他 /api 一樣經過 guard(本機 Host / Origin、POST 要 JSON、啟用 LDAP 時要登入)。
// officeWriteTimeout SSE 每次寫出的期限
var officeWriteTimeout = 10 * time.Second

func registerOffice(mux *http.ServeMux, hub *office.Hub) {
	mux.HandleFunc("GET /office", hub.ServeStatic)
	mux.HandleFunc("GET /office/", hub.ServeStatic)

	mux.HandleFunc("POST /api/office/command", func(w http.ResponseWriter, r *http.Request) {
		args, err := readArgs(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		wait := 0.0
		if v, ok := args["wait"]; ok {
			f, ok := v.(float64)
			if !ok || f < 0 || f > 120 {
				writeJSON(w, 400, map[string]any{"error": "wait 要是 0–120 的秒數"})
				return
			}
			wait = f
			delete(args, "wait")
		}
		req, err := office.RequestFromArgs(args)
		if err != nil {
			badRequest(w, err)
			return
		}
		cmds, err := hub.Submit(req)
		if err != nil {
			officeFail(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"result": officeCommandResult(r, hub, cmds, wait)})
	})

	mux.HandleFunc("GET /api/office/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, hub.State(0))
	})

	mux.HandleFunc("POST /api/office/report", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if err != nil {
			badRequest(w, err)
			return
		}
		var rep office.Report
		if err := json.Unmarshal(b, &rep); err != nil {
			badFormat(w, err)
			return
		}
		out, err := hub.Report(rep)
		if err != nil {
			officeFail(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"result": out})
	})

	mux.HandleFunc("GET /api/office/events", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}
		v, err := hub.Subscribe()
		if err != nil {
			w.Header().Set("Retry-After", "10")
			writeErr(w, r, 503, err)
			return
		}
		defer hub.Unsubscribe(v)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		// 每次寫出前設寫入期限:網路半斷時 Write 不會永遠卡住(不支援就降級成沒有期限)
		rc := http.NewResponseController(w)
		send := func(b []byte) error {
			_ = rc.SetWriteDeadline(time.Now().Add(officeWriteTimeout))
			if _, err := w.Write(b); err != nil {
				return err
			}
			if err := rc.Flush(); err != nil {
				if !errors.Is(err, http.ErrNotSupported) {
					return err
				}
				fl.Flush()
			}
			return nil
		}
		w.WriteHeader(200)
		if err := send([]byte("retry: 3000\n\n")); err != nil {
			return
		}
		ping := time.NewTicker(hub.Heartbeat())
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-v.Gone:
				return
			case m := <-v.C:
				if err := send(office.FormatSSE(m)); err != nil {
					return
				}
				if m.CmdID != "" {
					hub.Delivered(v, m.CmdID)
				}
			case <-ping.C:
				if err := send([]byte("event: ping\ndata: {}\n\n")); err != nil {
					return
				}
			}
		}
	})
}

func officeErrStatus(err error) int {
	var ve office.ValidationError
	switch {
	case errors.As(err, &ve):
		return 400
	default:
		return 503 // 找不到 office3d 資料夾、places.json 讀不出來
	}
}

func officeCommandResult(r *http.Request, hub *office.Hub, cmds []*office.Command, wait float64) map[string]any {
	ids := make([]string, 0, len(cmds))
	for _, c := range cmds {
		ids = append(ids, c.ID)
	}
	out := map[string]any{"ids": ids, "viewers": hub.Viewers()}
	if wait > 0 {
		var done bool
		cmds, done = hub.Wait(r.Context(), ids, time.Duration(wait*float64(time.Second)))
		out["waited"] = true
		out["finished"] = done
	}
	out["commands"] = cmds
	if len(ids) > 0 {
		out["id"] = ids[0]
	}
	return out
}
