package architecturecodec

import (
	"encoding/json"
	"math"
	"sort"
)

// The generic drawing is a layered (left-to-right) layout: data flows from
// the left, every connection gets its own ports and channel tracks, long and
// backward connections pass through reserved slots, so no line runs under a
// box and no two lines share a segment.
//
// The drawing is compact so it reads at fit: each column is as wide as its
// longest part name, ports and slots are tight, and the tracks of one gap
// share a bounded band (the browser draws connections as curves through the
// slots, so tracks only keep the stored geometry unambiguous).
const (
	minNodeWidth = 150.0
	maxNodeWidth = 260.0
	nameAdvance  = 8.6 // horizontal room per rune of a part name
	namePadding  = 36.0
	nodeHeight   = 72.0
	portPitch    = 12.0 // minimum vertical distance between ports on one side
	slotHeight   = 12.0 // a reserved slot for a line crossing a column
	rowGap       = 56.0
	slotGap      = 10.0 // vertical gap next to a slot
	margin       = 72.0
	trackPitch   = 14.0
	trackBand    = 64.0 // most horizontal room the tracks of one gap take
	labelRoom    = 40.0 // horizontal room kept for captions on both sides of the tracks
	loopReach    = 22.0
	captionLift  = 7.0
)

// nameWidth is the box width a part name needs on one line, bounded.
func nameWidth(name string) float64 {
	return math.Min(maxNodeWidth, math.Max(minNodeWidth, math.Ceil(namePadding+nameAdvance*float64(len([]rune(name))))))
}

type layoutItem struct {
	id       string // component id, or "" for a slot
	layer    int
	order    int
	y        float64
	height   float64
	edges    []int // chains passing through or touching this item
	isSlot   bool
	chainPos int // for slots: index inside its chain
}

type layoutChain struct {
	edge     int // index into connections
	items    []int
	backward bool // drawn right-to-left
}

type layoutConn struct{ id, source, target, caption string }

// GenericLayouts places declared objects; it never supplies missing topology.
// Each level of the architecture gets its own drawing: the top-level parts in
// TopView, and the parts inside a component in its sub-architecture view.
// A connection is drawn once, in the view of the lowest part that contains
// both ends, between the parts of that view that hold them.
func GenericLayouts(proposal json.RawMessage) (map[string]Layout, error) {
	a, err := decodeObject(proposal)
	if err != nil {
		return nil, err
	}
	if err = validateArchitecture(a); err != nil {
		return nil, err
	}
	parents := ParentsOf(a)
	members := map[string][]string{}
	var order []string
	for _, value := range a["components"].([]any) {
		id := value.(map[string]any)["id"].(string)
		view := ViewFor(parents[id])
		if _, seen := members[view]; !seen {
			order = append(order, view)
		}
		members[view] = append(members[view], id)
	}
	conns := map[string][]layoutConn{}
	for _, value := range a["connections"].([]any) {
		c := value.(map[string]any)
		id, source, target, caption := c["id"].(string), c["source"].(string), c["target"].(string), c["label"].(string)
		if source == target {
			view := ViewFor(parents[source])
			conns[view] = append(conns[view], layoutConn{id, source, source, caption})
			continue
		}
		view, from, to, _ := DrawnBetween(parents, source, target)
		conns[view] = append(conns[view], layoutConn{id, from, to, caption})
	}
	names := map[string]string{}
	for _, value := range a["components"].([]any) {
		c := value.(map[string]any)
		names[c["id"].(string)] = c["name"].(string)
	}
	views := map[string]Layout{}
	for _, view := range order {
		views[view] = layeredView(members[view], conns[view], names)
	}
	return views, nil
}

// layeredView draws one level: ids in declaration order and the connections
// between them.
func layeredView(ids []string, conns []layoutConn, names map[string]string) Layout {
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}

	// 1. Break cycles: a depth-first walk in declaration order marks edges that
	// return to a node still on the stack as backward.
	out := make([][]int, len(ids))
	for e, c := range conns {
		if c.source != c.target {
			out[index[c.source]] = append(out[index[c.source]], e)
		}
	}
	backward := make([]bool, len(conns))
	state := make([]int, len(ids)) // 0 new, 1 on stack, 2 done
	var visit func(int)
	visit = func(n int) {
		state[n] = 1
		for _, e := range out[n] {
			t := index[conns[e].target]
			if state[t] == 1 {
				backward[e] = true
			} else if state[t] == 0 {
				visit(t)
			}
		}
		state[n] = 2
	}
	for n := range ids {
		if state[n] == 0 {
			visit(n)
		}
	}

	// 2. Longest-path layers over the acyclic orientation.
	layer := make([]int, len(ids))
	for changed := true; changed; {
		changed = false
		for e, c := range conns {
			if c.source == c.target {
				continue
			}
			s, t := index[c.source], index[c.target]
			if backward[e] {
				s, t = t, s
			}
			if layer[t] < layer[s]+1 {
				layer[t] = layer[s] + 1
				changed = true
			}
		}
	}
	// Pull sources next to the first consumer so leaves do not sit far left.
	for n := range ids {
		next := math.MaxInt
		incoming := false
		for e, c := range conns {
			if c.source == c.target {
				continue
			}
			s, t := index[c.source], index[c.target]
			if backward[e] {
				s, t = t, s
			}
			if t == n {
				incoming = true
			}
			if s == n && layer[t] < next {
				next = layer[t]
			}
		}
		if !incoming && next != math.MaxInt && next-1 > layer[n] {
			layer[n] = next - 1
		}
	}
	layers := 0
	for _, l := range layer {
		layers = max(layers, l+1)
	}

	// 3. Items: one per component plus one slot per crossed column.
	items := []layoutItem{}
	nodeItem := make([]int, len(ids))
	for n, id := range ids {
		nodeItem[n] = len(items)
		items = append(items, layoutItem{id: id, layer: layer[n], height: nodeHeight})
	}
	chains := make([]layoutChain, len(conns))
	for e, c := range conns {
		s, t := nodeItem[index[c.source]], nodeItem[index[c.target]]
		chain := layoutChain{edge: e, backward: backward[e]}
		if c.source == c.target {
			chain.items = []int{s}
			items[s].edges = append(items[s].edges, e)
			chains[e] = chain
			continue
		}
		lo, hi := s, t
		if backward[e] {
			lo, hi = t, s
		}
		chain.items = append(chain.items, lo)
		for l := items[lo].layer + 1; l < items[hi].layer; l++ {
			chain.items = append(chain.items, len(items))
			items = append(items, layoutItem{layer: l, height: slotHeight, isSlot: true, chainPos: len(chain.items) - 1})
		}
		chain.items = append(chain.items, hi)
		for _, it := range chain.items {
			items[it].edges = append(items[it].edges, e)
		}
		chains[e] = chain
	}

	// 4. Order inside columns: declaration order, then barycentre sweeps.
	columns := make([][]int, layers)
	for i, it := range items {
		columns[it.layer] = append(columns[it.layer], i)
	}
	for _, col := range columns {
		for k, it := range col {
			items[it].order = k
		}
	}
	neighbours := func(it, side int) []int {
		var result []int
		for _, e := range items[it].edges {
			ch := chains[e].items
			for k, x := range ch {
				if x != it {
					continue
				}
				if side < 0 && k > 0 {
					result = append(result, ch[k-1])
				}
				if side > 0 && k+1 < len(ch) {
					result = append(result, ch[k+1])
				}
			}
		}
		return result
	}
	sweep := func(l, side int) {
		col := columns[l]
		score := map[int]float64{}
		for _, it := range col {
			ns := neighbours(it, side)
			if len(ns) == 0 {
				score[it] = float64(items[it].order)
				continue
			}
			sum := 0.0
			for _, n := range ns {
				sum += float64(items[n].order)
			}
			score[it] = sum / float64(len(ns))
		}
		sort.SliceStable(col, func(i, j int) bool { return score[col[i]] < score[col[j]] })
		for k, it := range col {
			items[it].order = k
		}
	}
	// crossings counts line crossings between neighbouring columns.
	crossings := func() int {
		total := 0
		for l := 0; l+1 < layers; l++ {
			var segs [][2]int
			for _, it := range columns[l] {
				for _, n := range neighbours(it, 1) {
					segs = append(segs, [2]int{items[it].order, items[n].order})
				}
			}
			for i := range segs {
				for j := i + 1; j < len(segs); j++ {
					a, b := segs[i], segs[j]
					if (a[0] < b[0] && a[1] > b[1]) || (a[0] > b[0] && a[1] < b[1]) {
						total++
					}
				}
			}
		}
		return total
	}
	saveOrder := func() []int {
		orders := make([]int, len(items))
		for i := range items {
			orders[i] = items[i].order
		}
		return orders
	}
	restoreOrder := func(orders []int) {
		for i := range items {
			items[i].order = orders[i]
		}
		for _, col := range columns {
			sort.SliceStable(col, func(i, j int) bool { return items[col[i]].order < items[col[j]].order })
		}
	}
	// transpose swaps neighbours in a column while that removes crossings.
	pairCross := func(u, v int) int {
		count := 0
		for _, side := range []int{-1, 1} {
			for _, a := range neighbours(u, side) {
				for _, b := range neighbours(v, side) {
					if items[a].order > items[b].order {
						count++
					}
				}
			}
		}
		return count
	}
	transpose := func() {
		for improved, pass := true, 0; improved && pass < 8; pass++ {
			improved = false
			for _, col := range columns {
				for k := 0; k+1 < len(col); k++ {
					u, v := col[k], col[k+1]
					if pairCross(u, v) > pairCross(v, u) {
						col[k], col[k+1] = v, u
						items[u].order, items[v].order = k+1, k
						improved = true
					}
				}
			}
		}
	}
	best, bestOrder := crossings(), saveOrder()
	for round := 0; round < 16; round++ {
		if round%2 == 0 {
			for l := 1; l < layers; l++ {
				sweep(l, -1)
			}
		} else {
			for l := layers - 2; l >= 0; l-- {
				sweep(l, 1)
			}
		}
		transpose()
		if c := crossings(); c < best {
			best, bestOrder = c, saveOrder()
		}
	}
	restoreOrder(bestOrder)

	// 5. Ports: a node side carries one port per touching line.
	type port struct {
		edge int
		end  int // 0 start of chain, 1 end of chain
	}
	sides := map[[2]int][]port{} // key: item, side (-1 left, +1 right, 0 top)
	for e, ch := range chains {
		if len(ch.items) == 1 {
			it := ch.items[0]
			sides[[2]int{it, 1}] = append(sides[[2]int{it, 1}], port{e, 0})
			sides[[2]int{it, 0}] = append(sides[[2]int{it, 0}], port{e, 1})
			continue
		}
		first, last := ch.items[0], ch.items[len(ch.items)-1]
		sides[[2]int{first, 1}] = append(sides[[2]int{first, 1}], port{e, 0})
		sides[[2]int{last, -1}] = append(sides[[2]int{last, -1}], port{e, 1})
	}
	for i := range items {
		if items[i].isSlot {
			continue
		}
		n := max(len(sides[[2]int{i, -1}]), len(sides[[2]int{i, 1}]))
		items[i].height = math.Max(nodeHeight, portPitch*float64(n+1))
	}

	gapBetween := func(a, b int) float64 {
		if items[a].isSlot || items[b].isSlot {
			return slotGap
		}
		return rowGap
	}
	// Each column is as wide as its longest name.
	colWidth := make([]float64, layers)
	for n, id := range ids {
		colWidth[layer[n]] = math.Max(colWidth[layer[n]], nameWidth(names[id]))
	}
	for l := range colWidth {
		if colWidth[l] == 0 {
			colWidth[l] = minNodeWidth
		}
	}

	// 6. Vertical placement: pull towards neighbours, keep order and gaps.
	// place fits the column to the desired centres in the least-squares sense
	// while keeping order and gaps (pool-adjacent-violators on top-offset).
	place := func(l int, desired map[int]float64) {
		col := append([]int(nil), columns[l]...)
		sort.Slice(col, func(i, j int) bool { return items[col[i]].order < items[col[j]].order })
		offset := make([]float64, len(col))
		for k := 1; k < len(col); k++ {
			offset[k] = offset[k-1] + items[col[k-1]].height + gapBetween(col[k-1], col[k])
		}
		type block struct {
			sum   float64
			count int
		}
		var blocks []block
		for k, it := range col {
			blocks = append(blocks, block{desired[it] - items[it].height/2 - offset[k], 1})
			for len(blocks) > 1 {
				a, b := blocks[len(blocks)-2], blocks[len(blocks)-1]
				if a.sum/float64(a.count) <= b.sum/float64(b.count) {
					break
				}
				blocks = append(blocks[:len(blocks)-2], block{a.sum + b.sum, a.count + b.count})
			}
		}
		k := 0
		for _, b := range blocks {
			for n := 0; n < b.count; n++ {
				items[col[k]].y = b.sum/float64(b.count) + offset[k]
				k++
			}
		}
	}
	for l := 0; l < layers; l++ {
		desired := map[int]float64{}
		y := 0.0
		col := append([]int(nil), columns[l]...)
		sort.Slice(col, func(i, j int) bool { return items[col[i]].order < items[col[j]].order })
		for k, it := range col {
			if k > 0 {
				y += gapBetween(col[k-1], it)
			}
			desired[it] = y + items[it].height/2
			y += items[it].height
		}
		place(l, desired)
	}
	centre := func(it int) float64 { return items[it].y + items[it].height/2 }
	for round := 0; round < 16; round++ {
		forward := round%2 == 0
		for k := 0; k < layers; k++ {
			l := k
			if !forward {
				l = layers - 1 - k
			}
			desired := map[int]float64{}
			for _, it := range columns[l] {
				ns := append(neighbours(it, -1), neighbours(it, 1)...)
				if len(ns) == 0 {
					desired[it] = centre(it)
					continue
				}
				sum := 0.0
				for _, n := range ns {
					sum += centre(n)
				}
				desired[it] = sum / float64(len(ns))
			}
			place(l, desired)
		}
	}
	minY := math.Inf(1)
	for _, it := range items {
		minY = math.Min(minY, it.y)
	}
	for i := range items {
		items[i].y += margin - minY
	}

	// Port positions, ordered by where the line goes so lines do not cross at
	// the box.
	portY := map[[2]int]float64{} // (edge, end) -> y
	for key, ports := range sides {
		it, side := key[0], key[1]
		other := func(p port) float64 {
			ch := chains[p.edge].items
			if len(ch) == 1 {
				return centre(it)
			}
			if p.end == 0 {
				return centre(ch[1])
			}
			return centre(ch[len(ch)-2])
		}
		sort.SliceStable(ports, func(i, j int) bool { return other(ports[i]) < other(ports[j]) })
		if side == 0 {
			continue
		}
		for k, p := range ports {
			portY[[2]int{p.edge, p.end}] = items[it].y + items[it].height*float64(k+1)/float64(len(ports)+1)
		}
	}

	// 7. Columns: x positions with room for tracks and captions.
	type hop struct {
		edge     int
		from, to float64 // y on the left and right column
	}
	gaps := make([][]hop, layers)
	pointY := func(e, k int) float64 {
		ch := chains[e].items
		switch {
		case k == 0:
			return portY[[2]int{e, 0}]
		case k == len(ch)-1:
			return portY[[2]int{e, 1}]
		default:
			return centre(ch[k])
		}
	}
	for e, ch := range chains {
		for k := 0; k+1 < len(ch.items); k++ {
			gaps[items[ch.items[k]].layer] = append(gaps[items[ch.items[k]].layer], hop{e, pointY(e, k), pointY(e, k+1)})
		}
	}
	trackX := map[[2]int]float64{} // (edge, gap) -> x
	columnX := make([]float64, layers)
	x := margin
	for l := 0; l < layers; l++ {
		columnX[l] = x
		x += colWidth[l]
		if l == layers-1 {
			break
		}
		var bends []hop
		for _, h := range gaps[l] {
			if h.from != h.to {
				bends = append(bends, h)
			}
		}
		// A line that leaves at height y must turn before any line that
		// arrives at the same height, so their horizontal pieces never overlap.
		sort.SliceStable(bends, func(i, j int) bool {
			a, b := bends[i], bends[j]
			if a.from == b.to {
				return true
			}
			if b.from == a.to {
				return false
			}
			down := func(h hop) bool { return h.to > h.from }
			if down(a) != down(b) {
				return down(a)
			}
			if down(a) {
				return a.from > b.from
			}
			return a.from < b.from
		})
		pitch := trackPitch
		if len(bends) > 1 {
			pitch = math.Min(trackPitch, trackBand/float64(len(bends)-1))
		}
		width := 2*labelRoom + pitch*float64(max(len(bends)-1, 0))
		for k, h := range bends {
			trackX[[2]int{h.edge, l}] = x + labelRoom + pitch*float64(k)
		}
		x += width
	}
	width := x + margin
	height := 0.0
	for _, it := range items {
		height = math.Max(height, it.y+it.height)
	}
	height += margin

	// 8. Lines and captions.
	view := Layout{Version: "generic-v3", Width: width, Height: height, Nodes: []Node{}, Edges: []Edge{}}
	for n := range ids {
		it := items[nodeItem[n]]
		view.Nodes = append(view.Nodes, Node{ID: it.id, X: columnX[it.layer], Y: it.y, Width: colWidth[it.layer], Height: it.height})
	}
	for e, ch := range chains {
		c := conns[e]
		var points [][2]float64
		if len(ch.items) == 1 {
			it := items[ch.items[0]]
			left, top, w := columnX[it.layer], it.y, colWidth[it.layer]
			py := portY[[2]int{e, 0}]
			points = [][2]float64{{left + w, py}, {left + w + loopReach, py}, {left + w + loopReach, top - loopReach}, {left + w/2, top - loopReach}, {left + w/2, top}}
		} else {
			first := items[ch.items[0]]
			points = append(points, [2]float64{columnX[first.layer] + colWidth[first.layer], pointY(e, 0)})
			for k := 0; k+1 < len(ch.items); k++ {
				l := items[ch.items[k]].layer
				from, to := pointY(e, k), pointY(e, k+1)
				if from != to {
					tx := trackX[[2]int{e, l}]
					points = append(points, [2]float64{tx, from}, [2]float64{tx, to})
				}
				points = append(points, [2]float64{columnX[l+1], to})
				if k+1 < len(ch.items)-1 {
					points = append(points, [2]float64{columnX[l+1] + colWidth[l+1], to}) // through a slot
				}
			}
			if ch.backward {
				// Drawn from its source (right) to its target (left).
				for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
					points[i], points[j] = points[j], points[i]
				}
			}
		}
		clean := [][2]float64{}
		for _, p := range points {
			if len(clean) > 0 && clean[len(clean)-1] == p {
				continue
			}
			// Merge straight runs into one segment.
			if n := len(clean); n >= 2 && ((clean[n-2][0] == clean[n-1][0] && clean[n-1][0] == p[0]) || (clean[n-2][1] == clean[n-1][1] && clean[n-1][1] == p[1])) {
				clean[n-1] = p
				continue
			}
			clean = append(clean, p)
		}
		view.Edges = append(view.Edges, Edge{ID: c.id, Points: clean, Caption: c.caption})
	}
	placeCaptions(&view)
	return view
}

// placeCaptions puts each caption above the longest free horizontal piece of
// its line; when two captions would overlap, the later one moves to its next
// longest piece.
func placeCaptions(view *Layout) {
	type box struct{ x0, x1, y0, y1 float64 }
	var taken []box
	overlaps := func(b box) bool {
		for _, t := range taken {
			if b.x0 < t.x1 && t.x0 < b.x1 && b.y0 < t.y1 && t.y0 < b.y1 {
				return true
			}
		}
		for _, n := range view.Nodes {
			if b.x0 < n.X+n.Width && n.X < b.x1 && b.y0 < n.Y+n.Height && n.Y < b.y1 {
				return true
			}
		}
		return false
	}
	for i := range view.Edges {
		e := &view.Edges[i]
		type piece struct {
			a, b   [2]float64
			length float64
		}
		var pieces []piece
		for k := 1; k < len(e.Points); k++ {
			a, b := e.Points[k-1], e.Points[k]
			if a[1] == b[1] {
				pieces = append(pieces, piece{a, b, math.Abs(b[0] - a[0])})
			}
		}
		sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].length > pieces[j].length })
		textWidth := math.Min(6.2*float64(len([]rune(e.Caption))), 170)
		chosen := false
		for _, p := range pieces {
			mid := (p.a[0] + p.b[0]) / 2
			b := box{mid - textWidth/2, mid + textWidth/2, p.a[1] - captionLift - 11, p.a[1] - captionLift + 2}
			if !overlaps(b) {
				e.LabelAt = [2]float64{mid, p.a[1] - captionLift}
				taken = append(taken, b)
				chosen = true
				break
			}
		}
		if !chosen {
			p := e.Points[0]
			if len(pieces) > 0 {
				p = [2]float64{(pieces[0].a[0] + pieces[0].b[0]) / 2, pieces[0].a[1]}
			}
			e.LabelAt = [2]float64{p[0], p[1] - captionLift}
		}
	}
}
