package core

// 舊的 wireframe_svg 動作(只畫線框圖),保留相容;新的畫面用 diagram_svg(見 diagram.go)。

func init() {
	RegisterAction("wireframe_svg", func(_ *App, args map[string]any) (any, error) {
		return renderDiagram("wireframe", args["spec"]), nil
	})
}
