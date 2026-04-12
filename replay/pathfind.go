package replay

import (
	"container/heap"
	"math"

	"github.com/jxsl13/twmap"
)

// TilePos is a tile coordinate on the map grid.
type TilePos struct{ X, Y int }

// WorldToTile converts world coordinates to tile coordinates.
func WorldToTile(worldX, worldY int) TilePos {
	return TilePos{worldX / twmap.TileSize, worldY / twmap.TileSize}
}

// TileToWorld converts tile coordinates to world coordinates (center of tile).
func TileToWorld(p TilePos) (int, int) {
	return p.X*twmap.TileSize + twmap.TileSize/2, p.Y*twmap.TileSize + twmap.TileSize/2
}

// TileGrid provides tile lookups on the game layer for pathfinding.
type TileGrid struct {
	tiles []twmap.Tile
	W, H  int
}

// NewTileGrid extracts the game layer from a parsed map.
func NewTileGrid(m *twmap.Map) *TileGrid {
	layers := m.GameLayers()
	if len(layers) == 0 {
		return &TileGrid{}
	}
	gl := layers[0]
	return &TileGrid{tiles: gl.Tiles, W: gl.Width, H: gl.Height}
}

// At returns the tile ID at tile coordinates (tx, ty).
func (g *TileGrid) At(tx, ty int) uint8 {
	if tx < 0 || ty < 0 || tx >= g.W || ty >= g.H {
		return twmap.TileSolid
	}
	idx := ty*g.W + tx
	if idx >= len(g.tiles) {
		return twmap.TileSolid
	}
	return g.tiles[idx].ID
}

// isSolid reports whether the tile blocks movement.
func (g *TileGrid) isSolid(tx, ty int) bool {
	return twmap.IsSolid(g.At(tx, ty))
}

// isDangerous reports whether stepping on a tile kills or freezes the player.
func (g *TileGrid) isDangerous(tx, ty int) bool {
	id := g.At(tx, ty)
	return id == twmap.TileDeath || id == twmap.TileFreeze ||
		id == twmap.TileDeepFreeze || id == twmap.TileLiveFreeze
}

// isPassable reports whether a tile can be traversed.
func (g *TileGrid) isPassable(tx, ty int) bool {
	return !g.isSolid(tx, ty) && !g.isDangerous(tx, ty)
}

// IsPassable is the exported version of isPassable for use in tests.
func (g *TileGrid) IsPassable(tx, ty int) bool { return g.isPassable(tx, ty) }

// IsSolid is the exported version of isSolid for use in tests.
func (g *TileGrid) IsSolid(tx, ty int) bool { return g.isSolid(tx, ty) }

// Height returns the grid height in tiles.
func (g *TileGrid) Height() int { return g.H }

// FindPath uses A* to find a walkable path from start to goal tile
// coordinates. The search models Teeworlds gravity: a tee can walk
// left/right on ground, fall off edges, and jump up to 5 tiles high.
//
// Returns nil if no path is found.
func FindPath(grid *TileGrid, start, goal TilePos) []TilePos {
	open := &pathHeap{}
	heap.Init(open)

	gScore := map[TilePos]float64{start: 0}
	cameFrom := map[TilePos]TilePos{}
	closed := map[TilePos]bool{}

	h := func(p TilePos) float64 {
		dx := float64(p.X - goal.X)
		dy := float64(p.Y - goal.Y)
		return math.Abs(dx) + math.Abs(dy)
	}

	heap.Push(open, astarNode{p: start, g: 0, f: h(start)})

	for open.Len() > 0 {
		cur := heap.Pop(open).(astarNode)

		if cur.p == goal {
			path := []TilePos{goal}
			p := goal
			for p != start {
				p = cameFrom[p]
				path = append(path, p)
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return path
		}

		if closed[cur.p] {
			continue
		}
		closed[cur.p] = true

		for _, nb := range pathNeighbors(grid, cur.p) {
			if closed[nb] {
				continue
			}
			tentG := cur.g + 1
			if prev, ok := gScore[nb]; ok && tentG >= prev {
				continue
			}
			gScore[nb] = tentG
			cameFrom[nb] = cur.p
			heap.Push(open, astarNode{p: nb, g: tentG, f: tentG + h(nb)})
		}
	}

	return nil
}

// pathNeighbors returns reachable neighbor tiles from p, accounting for
// Teeworlds physics: walk, fall, jump, jump-across, and hook.
func pathNeighbors(grid *TileGrid, p TilePos) []TilePos {
	var result []TilePos

	onGround := grid.isSolid(p.X, p.Y+1)

	// Walk left/right (works in air too — air control)
	if grid.isPassable(p.X-1, p.Y) {
		result = append(result, TilePos{p.X - 1, p.Y})
	}
	if grid.isPassable(p.X+1, p.Y) {
		result = append(result, TilePos{p.X + 1, p.Y})
	}

	// Fall down (gravity)
	if !onGround {
		for dy := 1; dy <= 30; dy++ {
			ty := p.Y + dy
			if grid.isSolid(p.X, ty) {
				landing := TilePos{p.X, ty - 1}
				if grid.isPassable(landing.X, landing.Y) && landing != p {
					result = append(result, landing)
				}
				break
			}
			if grid.isDangerous(p.X, ty) {
				break
			}
		}
	}

	// Jump (only on ground)
	if onGround {
		const maxJumpHeight = 5

		// Straight up and diagonally (1 tile left/right)
		for _, dx := range []int{0, -1, 1} {
			for dy := 1; dy <= maxJumpHeight; dy++ {
				tx := p.X + dx
				ty := p.Y - dy
				if grid.isSolid(tx, ty) || grid.isDangerous(tx, ty) {
					break
				}
				if grid.isPassable(tx, ty) {
					result = append(result, TilePos{tx, ty})
				}
			}
		}

		// Jump across gaps: 2-4 tiles horizontal, 0-3 tiles up
		for _, dx := range []int{-1, 1} {
			for hDist := 2; hDist <= 4; hDist++ {
				tx := p.X + dx*hDist
				for vDist := 0; vDist <= 3; vDist++ {
					ty := p.Y - vDist
					if grid.isPassable(tx, ty) && grid.isSolid(tx, ty+1) {
						pathClear := true
						for step := 1; step < hDist; step++ {
							sx := p.X + dx*step
							if grid.isSolid(sx, p.Y) || grid.isSolid(sx, p.Y-1) {
								pathClear = false
								break
							}
						}
						if pathClear {
							result = append(result, TilePos{tx, ty})
						}
					}
				}
			}
		}
	}

	// Hook movement: can reach tiles by hooking onto a nearby solid/hookable
	// surface and swinging. Hook range is ~12 tiles. We check for reachable
	// positions where there exists a hookable solid tile in line-of-sight.
	result = append(result, hookNeighbors(grid, p)...)

	return result
}

// hookNeighbors returns positions reachable by hooking from p.
// The hook can reach solid/unhookable surfaces up to hookRange tiles away.
// We model this as: if there's a solid tile within range that has
// line-of-sight from p, we can reach passable tiles near that anchor.
func hookNeighbors(grid *TileGrid, p TilePos) []TilePos {
	const hookRange = 10
	var result []TilePos
	seen := map[TilePos]bool{}

	// Scan for hookable solid tiles within range.
	for dy := -hookRange; dy <= hookRange; dy++ {
		for dx := -hookRange; dx <= hookRange; dx++ {
			ax, ay := p.X+dx, p.Y+dy
			dist := dx*dx + dy*dy
			if dist > hookRange*hookRange || dist < 4 {
				continue
			}
			if !grid.isSolid(ax, ay) {
				continue
			}
			// Unhookable tiles can't be hooked.
			if grid.At(ax, ay) == twmap.TileUnhookable {
				continue
			}
			// Check line-of-sight (no solid blocking the hook line).
			if !hasLineOfSight(grid, p.X, p.Y, ax, ay) {
				continue
			}
			// This solid tile is hookable. The tee can swing to positions
			// around and below the anchor. Model reachable tiles:
			// - Tiles near the anchor (within 3 tiles) that are passable
			//   and have solid ground below.
			// - Tiles between p and anchor that are passable with ground.
			for ny := -3; ny <= 3; ny++ {
				for nx := -3; nx <= 3; nx++ {
					tx, ty := ax+nx, ay+ny
					if tx == p.X && ty == p.Y {
						continue
					}
					if seen[TilePos{tx, ty}] {
						continue
					}
					if !grid.isPassable(tx, ty) {
						continue
					}
					// Must have ground to land on, or be directly adjacent to the anchor.
					hasGround := ty+1 < grid.H && grid.isSolid(tx, ty+1)
					if !hasGround {
						continue
					}
					seen[TilePos{tx, ty}] = true
					result = append(result, TilePos{tx, ty})
				}
			}
		}
	}
	return result
}

// hasLineOfSight checks if a straight line between two tile positions
// is free of solid obstacles (Bresenham-style ray march).
func hasLineOfSight(grid *TileGrid, x0, y0, x1, y1 int) bool {
	dx := x1 - x0
	dy := y1 - y0
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	x, y := x0, y0
	for {
		// Skip start and end tiles.
		if !(x == x0 && y == y0) && !(x == x1 && y == y1) {
			if grid.isSolid(x, y) {
				return false
			}
		}
		if x == x1 && y == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x += sx
		}
		if e2 < dx {
			err += dx
			y += sy
		}
	}
	return true
}

// --- A* priority queue ---

type astarNode struct {
	p    TilePos
	g, f float64
}

type pathHeap []astarNode

func (h pathHeap) Len() int           { return len(h) }
func (h pathHeap) Less(i, j int) bool { return h[i].f < h[j].f }
func (h pathHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *pathHeap) Push(x any) {
	*h = append(*h, x.(astarNode))
}

func (h *pathHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
