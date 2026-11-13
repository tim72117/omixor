package flowchart

import (
	"fmt"
	"math"
	"sort"
)

// 分層排版(Sugiyama 簡化版)。排版在抽象座標裡做:u 是「同一層裡」的方向,v 是「層與層」的方向;
// TB 時 u = x、v = y,LR 時 u = y、v = x,BT / RL 再把 v 反過來。
//
//  1. 去環:從來源節點做 DFS,指回堆疊上節點的邊(回邊)在排版時暫時反轉。
//  2. 分層:最長路徑法(拓撲順序 rank[v] = max(rank[u]+1)),沒有前驅的節點往下拉到緊貼後繼的上一層。
//  3. 長邊每經過一層插一個虛擬點;回邊在兩端所在的層也各插一個「掛在節點旁邊」的虛擬點,讓它從側邊繞回去。
//  4. 排序:DFS 順序起始,上下來回用重心法(barycenter)重排,保留交叉最少的一次;同一群組的成員在每層保持相鄰。
//  5. 座標:每層依「前驅/後繼的平均位置」求理想位置,在「依序且保持最小間距」的限制下用保序迴歸(PAVA)取最近解,上下來回幾輪。
//  6. 邊:層間用正交折線,每個層間的水平段分配到不同軌道避免重疊;同一個起點(或同一個入口)的線共用軌道。

const (
	nodeFS   = 14.0 // 節點字級
	edgeFS   = 12.0 // 邊標籤字級
	groupFS  = 12.0 // 群組標題字級
	nodeMaxW = 220.0
	skewIO   = 14.0 // 平行四邊形的斜度
	hexIn    = 14.0 // 六角形左右尖角的深度
	cylRY    = 7.0  // 圓柱上下橢圓的半高
	trackGap = 10.0
)

type vec struct{ u, v float64 }

type lnode struct {
	n            *Node // nil = 虛擬點
	rank, pos    int
	u            float64
	b, d         float64 // 抽象尺寸:b 沿 u、d 沿 v
	w, h         float64 // 實際寬高
	lines        []string
	chain        []int // 所在群組(由外而內)
	anchor       int   // 回邊兩端的虛擬點:掛在哪個節點旁邊;其他 -1
	preds, succs []int
	extra        float64 // 自環在 +u 側要留的空間
}

type ledge struct {
	e          *Edge
	from, to   int
	back, self bool
	path       []int // 正向:[from, 虛擬點…, to];回邊:rank(to) 到 rank(from) 的虛擬點(依層由小到大)
	segs       []*seg
	lines      []string
	lw, lh     float64 // 標籤框的實際寬高
	labelDummy int
	port       float64
	portKey    string
	pts        []vec
	lab        vec
}

// seg 一條邊跨過一個層間的部分
type seg struct {
	gap        int
	uTop, uBot float64
	kTop, kBot string
	track      int
}

type layout struct {
	g                          *Graph
	horiz                      bool // LR / RL
	flip                       bool // BT / RL
	nodes                      []*lnode
	edges                      []*ledge
	layers                     [][]int
	gpar                       []int // 群組的上層群組(-1 = 最外層)
	gidx                       map[string]int
	gmin                       []int // 群組成員的最小、最大層
	gmax                       []int
	depth                      []float64 // 每層的厚度
	vc                         []float64 // 每層中心的 v
	endPad                     []float64 // 層 k 下方:結束的群組框要留的空間
	staPad                     []float64 // 層 k 上方:開始的群組框要留的空間(含標題)
	tracks                     []int
	gapLab                     []float64 // 層間要放的標籤最大厚度
	padSU, padEU, padSV, padEV float64
}

func newLayout(g *Graph) *layout {
	L := &layout{g: g, horiz: g.Direction == "LR" || g.Direction == "RL", flip: g.Direction == "BT" || g.Direction == "RL", gidx: map[string]int{}}
	// 群組框的內距:標題那一側 30,其他 14
	L.padSU, L.padEU, L.padSV, L.padEV = 14, 14, 30, 14
	if L.horiz {
		L.padSU, L.padSV = 30, 14
	} else if L.flip {
		L.padSV, L.padEV = 14, 30
	}
	for i, gr := range g.Groups {
		L.gidx[gr.ID] = i
	}
	L.gpar = make([]int, len(g.Groups))
	for i, gr := range g.Groups {
		L.gpar[i] = -1
		if gr.Parent != "" {
			L.gpar[i] = L.gidx[gr.Parent]
		}
	}
	return L
}

// chainOf 群組 id → 由外而內的群組索引
func (L *layout) chainOf(group string) []int {
	if group == "" {
		return nil
	}
	out := []int{}
	for i := L.gidx[group]; i >= 0; i = L.gpar[i] {
		out = append([]int{i}, out...)
		if len(out) > len(L.gpar) {
			break
		}
	}
	return out
}

func commonPrefix(a, b []int) []int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return a[:n:n]
}

// sizeNode 依標籤估算節點大小
func sizeNode(n *Node) (lines []string, w, h float64) {
	lh := nodeFS * lineMult
	switch n.Shape {
	case "decision":
		lines = wrap(n.Label, 132, nodeFS, 5)
		tw, th := linesW(lines, nodeFS), lh*float64(len(lines))
		if len(lines) == 1 {
			w, h = tw*1.5+30, th*2.4+24
		} else {
			w, h = tw*1.6+30, th*2+28
		}
		return lines, math.Max(w, 100), math.Max(h, 60)
	case "circle":
		lines = wrap(n.Label, 120, nodeFS, 4)
		tw, th := linesW(lines, nodeFS), lh*float64(len(lines))
		dia := math.Max(math.Max(tw, th)*1.25+20, 64)
		return lines, dia, dia
	}
	padW := 28.0
	switch n.Shape {
	case "io":
		padW += skewIO + 6
	case "subprocess":
		padW += 16
	case "hexagon":
		padW += hexIn * 2
	case "note":
		padW += 8
	}
	lines = wrap(n.Label, nodeMaxW-padW, nodeFS, 6)
	tw, th := linesW(lines, nodeFS), lh*float64(len(lines))
	w, h = tw+padW, th+20
	minW := 80.0
	switch n.Shape {
	case "start", "end":
		h = th + 18
		w = tw + math.Min(h, 40) + 12
		minW = 90
	case "database":
		h = th + 20 + cylRY*2
	}
	return lines, math.Max(w, minW), math.Max(h, 40)
}

func edgeLabel(s string) (lines []string, w, h float64) {
	if s == "" {
		return nil, 0, 0
	}
	lines = wrap(s, 132, edgeFS, 3)
	return lines, linesW(lines, edgeFS) + 10, edgeFS*lineMult*float64(len(lines)) + 4
}

// labelB / labelD 標籤框沿 u、沿 v 的大小
func (L *layout) labelB(e *ledge) float64 {
	if L.horiz {
		return e.lh
	}
	return e.lw
}

func (L *layout) labelD(e *ledge) float64 {
	if L.horiz {
		return e.lw
	}
	return e.lh
}

func (L *layout) addDummy(rank int, chain []int, anchor int) int {
	L.nodes = append(L.nodes, &lnode{rank: rank, b: 10, chain: chain, anchor: anchor})
	return len(L.nodes) - 1
}

func (L *layout) link(a, b int) {
	L.nodes[a].succs = append(L.nodes[a].succs, b)
	L.nodes[b].preds = append(L.nodes[b].preds, a)
}

// build 去環、分層、插虛擬點
func (L *layout) build() {
	g := L.g
	idx := map[string]int{}
	for i, n := range g.Nodes {
		ln := &lnode{n: n, anchor: -1, chain: L.chainOf(n.Group)}
		ln.lines, ln.w, ln.h = sizeNode(n)
		ln.b, ln.d = ln.w, ln.h
		if L.horiz {
			ln.b, ln.d = ln.h, ln.w
		}
		L.nodes = append(L.nodes, ln)
		idx[n.ID] = i
	}
	N := len(g.Nodes)
	out := make([][]int, N) // 每個節點出去的邊(邊的索引)
	indeg := make([]int, N)
	for _, e := range g.Edges {
		le := &ledge{e: e, from: idx[e.From], to: idx[e.To], labelDummy: -1}
		le.self = le.from == le.to
		le.lines, le.lw, le.lh = edgeLabel(e.Label)
		L.edges = append(L.edges, le)
		if !le.self {
			out[le.from] = append(out[le.from], len(L.edges)-1)
			indeg[le.to]++
		}
	}
	// 1. 去環
	state := make([]int, N)
	var dfs func(v int)
	dfs = func(v int) {
		state[v] = 1
		for _, ei := range out[v] {
			e := L.edges[ei]
			switch state[e.to] {
			case 0:
				dfs(e.to)
			case 1:
				e.back = true
			}
		}
		state[v] = 2
	}
	for v := 0; v < N; v++ {
		if indeg[v] == 0 && state[v] == 0 {
			dfs(v)
		}
	}
	for v := 0; v < N; v++ {
		if state[v] == 0 {
			dfs(v)
		}
	}
	// 2. 分層(DAG 上的最長路徑)
	dsucc := make([][]int, N)
	dpred := make([][]int, N)
	for _, e := range L.edges {
		if e.self {
			continue
		}
		a, b := e.from, e.to
		if e.back {
			a, b = b, a
		}
		dsucc[a] = append(dsucc[a], b)
		dpred[b] = append(dpred[b], a)
	}
	in := make([]int, N)
	for v := 0; v < N; v++ {
		in[v] = len(dpred[v])
	}
	topo := []int{}
	done := make([]bool, N)
	for len(topo) < N {
		progressed := false
		for v := 0; v < N; v++ {
			if !done[v] && in[v] == 0 {
				done[v] = true
				topo = append(topo, v)
				for _, w := range dsucc[v] {
					in[w]--
				}
				progressed = true
			}
		}
		if !progressed { // 理論上不會發生(去環後是 DAG);保險起見把剩下的照順序放
			for v := 0; v < N; v++ {
				if !done[v] {
					done[v] = true
					topo = append(topo, v)
				}
			}
		}
	}
	rank := make([]int, N)
	for _, v := range topo {
		for _, w := range dsucc[v] {
			rank[w] = max(rank[w], rank[v]+1)
		}
	}
	for i := len(topo) - 1; i >= 0; i-- { // 沒有前驅的節點拉到緊貼後繼
		v := topo[i]
		if len(dpred[v]) == 0 && len(dsucc[v]) > 0 {
			m := math.MaxInt
			for _, w := range dsucc[v] {
				m = min(m, rank[w])
			}
			rank[v] = m - 1
		}
	}
	maxRank := 0
	for v := 0; v < N; v++ {
		L.nodes[v].rank = rank[v]
		maxRank = max(maxRank, rank[v])
	}
	// 群組的層範圍(含子群組的成員)
	L.gmin = make([]int, len(g.Groups))
	L.gmax = make([]int, len(g.Groups))
	for i := range L.gmin {
		L.gmin[i], L.gmax[i] = math.MaxInt, -1
	}
	for v := 0; v < N; v++ {
		for _, gi := range L.nodes[v].chain {
			L.gmin[gi] = min(L.gmin[gi], rank[v])
			L.gmax[gi] = max(L.gmax[gi], rank[v])
		}
	}
	// 3. 虛擬點
	for _, e := range L.edges {
		if e.self {
			n := L.nodes[e.from]
			x := 30.0
			if len(e.lines) > 0 {
				x += L.labelB(e) + 6
			}
			n.extra = math.Max(n.extra, x)
			continue
		}
		s, t := L.nodes[e.from], L.nodes[e.to]
		lca := commonPrefix(s.chain, t.chain)
		if !e.back {
			e.path = []int{e.from}
			prev := e.from
			for r := s.rank + 1; r < t.rank; r++ {
				d := L.addDummy(r, lca, -1)
				L.link(prev, d)
				e.path = append(e.path, d)
				prev = d
			}
			L.link(prev, e.to)
			e.path = append(e.path, e.to)
			continue
		}
		// 回邊:from 在下、to 在上;兩端所在的層也放掛在旁邊的虛擬點
		prev := -1
		for r := t.rank; r <= s.rank; r++ {
			anchor, ch := -1, lca
			if r == t.rank {
				anchor, ch = e.to, t.chain
			} else if r == s.rank {
				anchor, ch = e.from, s.chain
			}
			d := L.addDummy(r, ch, anchor)
			if prev >= 0 {
				L.link(prev, d)
			}
			e.path = append(e.path, d)
			prev = d
		}
		if len(e.lines) > 0 && len(e.path) >= 3 {
			e.labelDummy = e.path[len(e.path)/2]
			L.nodes[e.labelDummy].b = L.labelB(e) + 8
		}
	}
	L.layers = make([][]int, maxRank+1)
	for i, n := range L.nodes {
		L.layers[n.rank] = append(L.layers[n.rank], i)
	}
}

// ---- 4. 排序 ----

func (L *layout) order() {
	// 起始順序:DFS 走訪順序
	key := make([]float64, len(L.nodes))
	seen := make([]bool, len(L.nodes))
	cnt := 0.0
	var dfs func(i int)
	dfs = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		key[i] = cnt
		cnt++
		for _, s := range L.nodes[i].succs {
			dfs(s)
		}
	}
	for i, n := range L.nodes {
		if n.n != nil && len(n.preds) == 0 {
			dfs(i)
		}
	}
	for i := range L.nodes {
		dfs(i)
	}
	for k := range L.layers {
		sort.SliceStable(L.layers[k], func(a, b int) bool { return key[L.layers[k][a]] < key[L.layers[k][b]] })
		L.arrange(k, key)
	}
	best := L.snapshot()
	bestC := L.crossings()
	for it := 0; it < 24 && bestC > 0; it++ {
		if it%2 == 0 {
			for k := 1; k < len(L.layers); k++ {
				L.sortLayer(k, true)
			}
		} else {
			for k := len(L.layers) - 2; k >= 0; k-- {
				L.sortLayer(k, false)
			}
		}
		if c := L.crossings(); c < bestC {
			best, bestC = L.snapshot(), c
		}
	}
	L.layers = best
	L.setPos()
}

func (L *layout) snapshot() [][]int {
	out := make([][]int, len(L.layers))
	for k, l := range L.layers {
		out[k] = append([]int(nil), l...)
	}
	return out
}

func (L *layout) setPos() {
	for _, l := range L.layers {
		for p, i := range l {
			L.nodes[i].pos = p
		}
	}
}

// sortLayer 依前驅(up)或後繼的平均位置重排一層
func (L *layout) sortLayer(k int, up bool) {
	L.setPos()
	key := make([]float64, len(L.nodes))
	for _, i := range L.layers[k] {
		n := L.nodes[i]
		nb := n.succs
		if up {
			nb = n.preds
		}
		if len(nb) == 0 {
			key[i] = float64(n.pos)
			continue
		}
		s := 0.0
		for _, j := range nb {
			s += float64(L.nodes[j].pos)
		}
		key[i] = s / float64(len(nb))
	}
	L.arrange(k, key)
}

// arrange 依 key 排一層:同群組相鄰,掛在節點旁的虛擬點緊接在該節點後面
func (L *layout) arrange(k int, key []float64) {
	movable := []int{}
	attached := map[int][]int{}
	for _, i := range L.layers[k] {
		if a := L.nodes[i].anchor; a >= 0 {
			attached[a] = append(attached[a], i)
		} else {
			movable = append(movable, i)
		}
	}
	sorted := L.clusterSort(movable, key, 0)
	out := make([]int, 0, len(L.layers[k]))
	for _, i := range sorted {
		out = append(out, i)
		out = append(out, attached[i]...)
	}
	L.layers[k] = out
	L.setPos()
}

func (L *layout) clusterSort(ids []int, key []float64, depth int) []int {
	type cl struct {
		sum    float64
		n      int
		items  []int
		single bool
	}
	cls := []*cl{}
	byGroup := map[int]*cl{}
	for _, i := range ids {
		ch := L.nodes[i].chain
		if len(ch) > depth {
			c := byGroup[ch[depth]]
			if c == nil {
				c = &cl{}
				byGroup[ch[depth]] = c
				cls = append(cls, c)
			}
			c.items = append(c.items, i)
			c.sum += key[i]
			c.n++
		} else {
			cls = append(cls, &cl{sum: key[i], n: 1, items: []int{i}, single: true})
		}
	}
	sort.SliceStable(cls, func(a, b int) bool { return cls[a].sum/float64(cls[a].n) < cls[b].sum/float64(cls[b].n) })
	out := make([]int, 0, len(ids))
	for _, c := range cls {
		if c.single {
			out = append(out, c.items...)
		} else {
			sort.SliceStable(c.items, func(a, b int) bool { return key[c.items[a]] < key[c.items[b]] })
			out = append(out, L.clusterSort(c.items, key, depth+1)...)
		}
	}
	return out
}

// crossings 相鄰層之間的交叉數(用樹狀陣列數逆序對)
func (L *layout) crossings() int {
	L.setPos()
	total := 0
	for k := 0; k+1 < len(L.layers); k++ {
		type pr struct{ a, b int }
		ps := []pr{}
		for _, i := range L.layers[k] {
			for _, s := range L.nodes[i].succs {
				ps = append(ps, pr{L.nodes[i].pos, L.nodes[s].pos})
			}
		}
		sort.Slice(ps, func(x, y int) bool {
			if ps[x].a != ps[y].a {
				return ps[x].a < ps[y].a
			}
			return ps[x].b < ps[y].b
		})
		n := len(L.layers[k+1])
		bit := make([]int, n+1)
		seenN := 0
		for _, p := range ps {
			// 已加入且 b 比我大的數量
			le := 0
			for x := p.b + 1; x > 0; x -= x & -x {
				le += bit[x]
			}
			total += seenN - le
			for x := p.b + 1; x <= n; x += x & -x {
				bit[x]++
			}
			seenN++
		}
	}
	return total
}

// ---- 5. 座標 ----

func (L *layout) sep(a, b *lnode) float64 {
	gap := 36.0
	if L.horiz {
		gap = 26
	}
	if a.n == nil && b.n == nil {
		gap = 12
	} else if a.n == nil || b.n == nil {
		gap = 18
	}
	c := len(commonPrefix(a.chain, b.chain))
	pad := float64(len(a.chain)-c)*L.padEU + float64(len(b.chain)-c)*L.padSU
	return a.b/2 + a.extra + gap + b.b/2 + pad
}

func (L *layout) coords() {
	for k := range L.layers {
		L.place(k, -1)
	}
	for it := 0; it < 16; it++ {
		if it%2 == 0 {
			for k := 1; k < len(L.layers); k++ {
				L.place(k, 0)
			}
		} else {
			for k := len(L.layers) - 2; k >= 0; k-- {
				L.place(k, 1)
			}
		}
	}
	for k := range L.layers {
		L.place(k, 2)
	}
	L.straighten()
	L.separateGroups()
	lo := math.Inf(1)
	for _, n := range L.nodes {
		lo = math.Min(lo, n.u-n.b/2)
	}
	for _, n := range L.nodes {
		n.u -= lo
	}
}

// place 一層的位置。mode:-1 緊密排列,0 依前驅,1 依後繼,2 兩者
func (L *layout) place(k int, mode int) {
	layer := L.layers[k]
	n := len(layer)
	des := make([]float64, n)
	wt := make([]float64, n)
	sp := make([]float64, n)
	for p, i := range layer {
		nd := L.nodes[i]
		if p+1 < n {
			sp[p] = L.sep(nd, L.nodes[layer[p+1]])
		}
		wt[p] = 1
		if nd.n == nil {
			wt[p] = 2
		}
		if mode < 0 {
			des[p] = 0
			continue
		}
		if nd.anchor >= 0 {
			a := L.nodes[nd.anchor]
			des[p] = a.u + a.b/2 + a.extra + 22 + nd.b/2
			continue
		}
		var nb []int
		switch mode {
		case 0:
			nb = nd.preds
		case 1:
			nb = nd.succs
		default:
			nb = append(append([]int{}, nd.preds...), nd.succs...)
		}
		if len(nb) == 0 {
			des[p] = nd.u
			continue
		}
		s := 0.0
		for _, j := range nb {
			s += L.nodes[j].u
		}
		des[p] = s / float64(len(nb))
	}
	us := pava(des, wt, sp)
	for p, i := range layer {
		L.nodes[i].u = us[p]
	}
}

// pava 在 x[i+1]-x[i] >= sep[i] 的限制下,求最接近 des(加權平方誤差)的位置
func pava(des, wt, sep []float64) []float64 {
	n := len(des)
	S := make([]float64, n)
	for i := 1; i < n; i++ {
		S[i] = S[i-1] + sep[i-1]
	}
	type blk struct {
		w, wt float64
		start int
	}
	st := []blk{}
	for i := 0; i < n; i++ {
		b := blk{wt[i], wt[i] * (des[i] - S[i]), i}
		for len(st) > 0 && st[len(st)-1].wt/st[len(st)-1].w > b.wt/b.w {
			p := st[len(st)-1]
			st = st[:len(st)-1]
			b = blk{p.w + b.w, p.wt + b.wt, p.start}
		}
		st = append(st, b)
	}
	out := make([]float64, n)
	for j, b := range st {
		end := n
		if j+1 < len(st) {
			end = st[j+1].start
		}
		m := b.wt / b.w
		for i := b.start; i < end; i++ {
			out[i] = m + S[i]
		}
	}
	return out
}

// ---- 6. 邊與層的位置 ----

// spreadable 入口可以沿著邊分散(入口那一側是平的)
func (L *layout) spreadHalf(n *lnode) float64 {
	if L.horiz {
		switch n.n.Shape {
		case "process", "subprocess", "note", "database":
			return n.b/2 - 8
		case "round":
			return n.b/2 - 12
		}
		return 0
	}
	switch n.n.Shape {
	case "process", "subprocess", "note":
		return n.b/2 - 10
	case "round":
		return n.b/2 - 14
	case "io":
		return n.b/2 - skewIO - 6
	case "start", "end":
		return (n.w-n.h)/2 - 4
	}
	return 0
}

// flowReach 節點中心沿 v 到邊界的距離(在 u 偏移 off 處)
func (L *layout) flowReach(n *lnode, off float64) float64 {
	hd := n.d / 2
	switch n.n.Shape {
	case "decision":
		return hd * math.Max(0, 1-math.Abs(off)/(n.b/2))
	case "circle":
		r := off / (n.b / 2)
		return hd * math.Sqrt(math.Max(0, 1-r*r))
	}
	if !L.horiz {
		return hd
	}
	switch n.n.Shape {
	case "start", "end":
		R := n.h / 2
		return n.w/2 - R + math.Sqrt(math.Max(0, R*R-off*off))
	case "hexagon":
		return n.w/2 - hexIn*math.Abs(off)/(n.h/2)
	case "io":
		return n.w/2 - skewIO/2
	}
	return hd
}

// sideReach 節點中心沿 +u 到邊界的距離(在 v 偏移 off 處)
func (L *layout) sideReach(n *lnode, off float64) float64 {
	hb := n.b / 2
	switch n.n.Shape {
	case "decision":
		return hb * math.Max(0, 1-math.Abs(off)/(n.d/2))
	case "circle":
		r := off / (n.d / 2)
		return hb * math.Sqrt(math.Max(0, 1-r*r))
	}
	if L.horiz {
		return hb
	}
	switch n.n.Shape {
	case "start", "end":
		R := n.h / 2
		return n.w/2 - R + math.Sqrt(math.Max(0, R*R-off*off))
	case "hexagon":
		return n.w/2 - hexIn*math.Abs(off)/(n.h/2)
	case "io":
		return n.w/2 - skewIO/2
	}
	return hb
}

func (L *layout) route() {
	K := len(L.layers)
	// 入口:同一個目標有多條正向邊進來、入口那一側是平的,就沿邊分散開
	incoming := map[int][]*ledge{}
	for _, e := range L.edges {
		if !e.self && !e.back && e.e.Style != "invisible" {
			incoming[e.to] = append(incoming[e.to], e)
		}
	}
	for t, es := range incoming {
		tn := L.nodes[t]
		sort.SliceStable(es, func(a, b int) bool {
			return L.nodes[es[a].path[len(es[a].path)-2]].u < L.nodes[es[b].path[len(es[b].path)-2]].u
		})
		half := L.spreadHalf(tn)
		m := len(es)
		step := 0.0
		if m > 1 && half > 0 {
			step = math.Min(18, 2*half/float64(m-1))
		}
		for j, e := range es {
			e.port = (float64(j) - float64(m-1)/2) * step
			e.portKey = fmt.Sprintf("p%d:%d", t, j)
			if step == 0 {
				e.portKey = fmt.Sprintf("p%d", t)
			}
		}
	}
	// 每個層間的線段
	gaps := make([][]*seg, max(K-1, 0))
	key := func(i int) string { return fmt.Sprintf("n%d", i) }
	for _, e := range L.edges {
		if e.self || e.e.Style == "invisible" {
			continue
		}
		last := len(e.path) - 1
		for j := 0; j < last; j++ {
			a, b := L.nodes[e.path[j]], L.nodes[e.path[j+1]]
			s := &seg{gap: a.rank, uTop: a.u, uBot: b.u, kTop: key(e.path[j]), kBot: key(e.path[j+1]), track: -1}
			if !e.back && j+1 == last {
				s.uBot, s.kBot = b.u+e.port, e.portKey
			}
			e.segs = append(e.segs, s)
			gaps[s.gap] = append(gaps[s.gap], s)
		}
	}
	// 分配軌道:共用端點的線段合成一組,組與組的水平範圍重疊就放不同軌道
	L.tracks = make([]int, max(K-1, 0))
	for k, ss := range gaps {
		par := map[string]string{}
		var find func(x string) string
		find = func(x string) string {
			if p, ok := par[x]; ok && p != x {
				r := find(p)
				par[x] = r
				return r
			}
			par[x] = x
			return x
		}
		bent := []*seg{}
		for _, s := range ss {
			if math.Abs(s.uTop-s.uBot) < 1 {
				continue
			}
			bent = append(bent, s)
			a, b := find(s.kTop), find(s.kBot)
			if a != b {
				par[a] = b
			}
		}
		type grp struct {
			lo, hi float64
			ss     []*seg
		}
		gm := map[string]*grp{}
		gl := []*grp{}
		for _, s := range bent {
			r := find(s.kTop)
			g := gm[r]
			if g == nil {
				g = &grp{lo: math.Inf(1), hi: math.Inf(-1)}
				gm[r] = g
				gl = append(gl, g)
			}
			g.lo = math.Min(g.lo, math.Min(s.uTop, s.uBot))
			g.hi = math.Max(g.hi, math.Max(s.uTop, s.uBot))
			g.ss = append(g.ss, s)
		}
		sort.SliceStable(gl, func(a, b int) bool { return gl[a].lo < gl[b].lo })
		ends := []float64{}
		for _, g := range gl {
			t := -1
			for i, e := range ends {
				if e+8 < g.lo {
					t = i
					break
				}
			}
			if t < 0 {
				ends = append(ends, 0)
				t = len(ends) - 1
			}
			ends[t] = g.hi
			for _, s := range g.ss {
				s.track = t
			}
		}
		L.tracks[k] = len(ends)
	}
	// 標籤所在的層間
	L.gapLab = make([]float64, max(K-1, 0))
	for _, e := range L.edges {
		if len(e.lines) == 0 || e.self || e.labelDummy >= 0 || len(e.segs) == 0 {
			continue
		}
		g := e.segs[0].gap
		L.gapLab[g] = math.Max(L.gapLab[g], L.labelD(e))
	}
	// 層的厚度、群組框的空間、層的位置
	L.depth = make([]float64, K)
	L.endPad = make([]float64, K)
	L.staPad = make([]float64, K)
	for k, l := range L.layers {
		for _, i := range l {
			n := L.nodes[i]
			if n.n == nil {
				continue
			}
			L.depth[k] = math.Max(L.depth[k], n.d)
			e, s := 0, 0
			for _, gi := range n.chain {
				if L.gmax[gi] == k {
					e++
				}
				if L.gmin[gi] == k {
					s++
				}
			}
			L.endPad[k] = math.Max(L.endPad[k], float64(e)*L.padEV)
			L.staPad[k] = math.Max(L.staPad[k], float64(s)*L.padSV)
		}
	}
	L.vc = make([]float64, K)
	if K > 0 {
		L.vc[0] = L.depth[0] / 2
	}
	minGap := 48.0
	if L.horiz {
		minGap = 56
	}
	for k := 0; k+1 < K; k++ {
		lower := 24.0
		if L.gapLab[k] > 0 {
			lower = L.gapLab[k] + 20
		}
		core := 14 + float64(max(L.tracks[k]-1, 0))*trackGap + lower
		gap := L.endPad[k] + L.staPad[k+1] + math.Max(minGap, core)
		L.vc[k+1] = L.vc[k] + L.depth[k]/2 + gap + L.depth[k+1]/2
	}
	// 路徑
	for _, e := range L.edges {
		switch {
		case e.self:
			n := L.nodes[e.from]
			R := n.u + L.sideReach(n, 8)
			v := L.vc[n.rank]
			e.pts = []vec{{R, v + 8}, {n.u + n.b/2 + 24, v + 8}, {n.u + n.b/2 + 24, v - 8}, {R, v - 8}}
			e.lab = vec{n.u + n.b/2 + 30 + L.labelB(e)/2, v}
		case e.e.Style == "invisible":
		case !e.back:
			s, t := L.nodes[e.from], L.nodes[e.to]
			e.pts = []vec{{s.u, L.vc[s.rank] + L.flowReach(s, 0)}}
			for _, sg := range e.segs {
				if sg.track >= 0 {
					tv := L.trackV(sg.gap, sg.track)
					e.pts = append(e.pts, vec{sg.uTop, tv}, vec{sg.uBot, tv})
				}
			}
			e.pts = append(e.pts, vec{t.u + e.port, L.vc[t.rank] - L.flowReach(t, e.port)})
			if len(e.lines) > 0 {
				e.lab = L.gapLabel(e.segs[0])
			}
		default:
			s, t := L.nodes[e.from], L.nodes[e.to]
			ds, dt := L.nodes[e.path[len(e.path)-1]], L.nodes[e.path[0]]
			e.pts = []vec{{s.u + L.sideReach(s, 0), L.vc[s.rank]}, {ds.u, L.vc[s.rank]}}
			for j := len(e.segs) - 1; j >= 0; j-- {
				sg := e.segs[j]
				if sg.track >= 0 {
					tv := L.trackV(sg.gap, sg.track)
					e.pts = append(e.pts, vec{sg.uBot, tv}, vec{sg.uTop, tv})
				}
			}
			e.pts = append(e.pts, vec{dt.u, L.vc[t.rank]}, vec{t.u + L.sideReach(t, 0), L.vc[t.rank]})
			if len(e.lines) > 0 {
				if e.labelDummy >= 0 {
					m := L.nodes[e.labelDummy]
					e.lab = vec{m.u, L.vc[m.rank]}
				} else {
					e.lab = L.gapLabel(e.segs[0])
				}
			}
		}
	}
}

func (L *layout) trackV(k, t int) float64 {
	return L.vc[k] + L.depth[k]/2 + L.endPad[k] + 14 + float64(t)*trackGap
}

// gapLabel 標籤放在層間:有折彎就放在折彎後往下那一段的中間,直線就放在層間中間
func (L *layout) gapLabel(s *seg) vec {
	k := s.gap
	lowEnd := L.vc[k+1] - L.depth[k+1]/2 - L.staPad[k+1]
	if s.track >= 0 {
		return vec{s.uBot, (L.trackV(k, s.track) + lowEnd) / 2}
	}
	top := L.vc[k] + L.depth[k]/2 + L.endPad[k]
	return vec{s.uTop, (top + lowEnd) / 2}
}

// canMove 把節點 i 移到 u 會不會和同一層左右的鄰居太近
func (L *layout) canMove(i int, u float64) bool {
	n := L.nodes[i]
	layer := L.layers[n.rank]
	if n.pos > 0 {
		l := L.nodes[layer[n.pos-1]]
		if u-l.u < L.sep(l, n)-0.01 {
			return false
		}
	}
	if n.pos+1 < len(layer) {
		r := L.nodes[layer[n.pos+1]]
		if r.u-u < L.sep(n, r)-0.01 {
			return false
		}
	}
	return true
}

// straighten 收尾:長邊的虛擬點盡量排成一直線;一對一相連的節點盡量對齊
func (L *layout) straighten() {
	for _, e := range L.edges {
		var ds []int
		cands := []float64{}
		switch {
		case e.self:
			continue
		case e.back:
			ds = e.path
			hi := math.Inf(-1)
			for _, d := range ds {
				hi = math.Max(hi, L.nodes[d].u)
			}
			cands = append(cands, hi)
		default:
			ds = e.path[1 : len(e.path)-1]
			cands = append(cands, L.nodes[e.from].u, L.nodes[e.to].u)
		}
		if len(ds) == 0 {
			continue
		}
		for _, d := range ds {
			cands = append(cands, L.nodes[d].u)
		}
		for _, u := range cands {
			ok := true
			for _, d := range ds {
				if !L.canMove(d, u) {
					ok = false
					break
				}
			}
			if ok {
				for _, d := range ds {
					L.nodes[d].u = u
				}
				break
			}
		}
	}
	// 一對一:A 只有一個後繼 B、B 只有一個前驅 A
	for k := 1; k < len(L.layers); k++ {
		for _, i := range L.layers[k] {
			n := L.nodes[i]
			if len(n.preds) != 1 {
				continue
			}
			p := L.nodes[n.preds[0]]
			if len(p.succs) != 1 || math.Abs(p.u-n.u) < 0.5 {
				continue
			}
			if L.canMove(i, p.u) {
				n.u = p.u
			} else if L.canMove(n.preds[0], n.u) {
				p.u = n.u
			}
		}
	}
}

// separateGroups 群組框是成員跨層的外框,不屬於群組、但落在框的層範圍內的節點要推到框外
func (L *layout) separateGroups() {
	G := len(L.gpar)
	if G == 0 {
		return
	}
	member := func(n *lnode, g int) bool {
		for _, c := range n.chain {
			if c == g {
				return true
			}
		}
		return false
	}
	nest := func(g int) int {
		d := 0
		for p := L.gpar[g]; p >= 0 && d <= G; p = L.gpar[p] {
			d++
		}
		return d
	}
	for round := 0; round < 6; round++ {
		moved := false
		for g := 0; g < G; g++ {
			if L.gmax[g] < 0 {
				continue
			}
			// 框的 u 範圍(含內距;巢狀的子群組框也在裡面,用最深的成員估)
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, n := range L.nodes {
				if n.n == nil || !member(n, g) {
					continue
				}
				extra := float64(len(n.chain) - nest(g) - 1)
				lo = math.Min(lo, n.u-n.b/2-L.padSU*(1+extra))
				hi = math.Max(hi, n.u+n.b/2+n.extra+L.padEU*(1+extra))
			}
			mid := (lo + hi) / 2
			for k := L.gmin[g]; k <= L.gmax[g]; k++ {
				layer := L.layers[k]
				first, last := -1, -1
				for p, i := range layer {
					if member(L.nodes[i], g) {
						if first < 0 {
							first = p
						}
						last = p
					}
				}
				for p, i := range layer {
					n := L.nodes[i]
					if member(n, g) {
						continue
					}
					right := p > last && last >= 0 || first < 0 && n.u >= mid
					if right {
						if d := hi + 18 - (n.u - n.b/2); d > 0.5 {
							for _, j := range layer[p:] {
								L.nodes[j].u += d
							}
							moved = true
						}
					} else if p < first || first < 0 {
						if d := (n.u + n.b/2 + n.extra) - (lo - 18); d > 0.5 {
							for _, j := range layer[:p+1] {
								L.nodes[j].u -= d
							}
							moved = true
						}
					}
				}
			}
		}
		if !moved {
			return
		}
	}
}
