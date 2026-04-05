package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

const (
	screenWidth  = 1200
	screenHeight = 800

	nutrientGridW = 120
	nutrientGridH = 80
	nutrientCellW = float64(screenWidth) / float64(nutrientGridW)
	nutrientCellH = float64(screenHeight) / float64(nutrientGridH)

	maxCells       = 2000
	initialCells   = 50
	seededCells    = 20
	maxAge         = 3000
	maxSize        = 15.0
	minSize        = 2.0
	maxSpeed       = 3.0
	maxDefense     = 1.0
	defenseDecay   = 0.005
	reproThreshold = 50.0
	diffusionRate  = 0.10
	nutrientDecay  = 0.01

	// Oxygen
	oxygenDiffusion  = 0.06  // oxygen mixes slowly — local depletion matters
	oxygenDecay      = 0.003 // slow natural decay
	oxygenSurfaceAdd = 0.04  // ambient oxygen added at surface each tick
	oxygenPhotoRate  = 0.1   // oxygen produced per unit of photosynthesis
	oxygenBreathRate = 0.15  // oxygen consumed per cell per tick (base)
	lowOxygenPenalty = 2.0   // heavy energy cost when local oxygen is near zero

	// Toxins
	toxinDiffusion = 0.03 // toxins spread slowly
	toxinDecay     = 0.02 // toxins break down over time
	toxinDamage    = 0.3  // energy damage per tick per unit of local toxin

	// Depth
	nutrientSinkRate = 0.08 // fraction of nutrients that drift down each tick
	depthCostMax     = 0.3  // extra maintenance cost at the very bottom
)

// Spatial hash bucket size
const bucketSize = 30.0

// --- Genetic language constants ---

const (
	geneSTART = 0
	geneSTOP  = 15
)

// SENSE
const (
	senseLight    = 1
	senseEnergy   = 2
	senseNeighbor = 3
	senseDist     = 4
	senseNutrient = 5
	senseAge      = 6
	senseSize     = 7
	senseOxygen   = 8
	senseToxin    = 9
)

// CONDITION
const (
	condHigh   = 1
	condLow    = 2
	condMedium = 3
	condAlways = 4
)

// ACTION
const (
	actPhoto      = 1
	actForward    = 2 // move in facing direction
	actTurnLeft   = 3 // rotate heading counter-clockwise
	actTurnRight  = 4 // rotate heading clockwise
	actEat        = 5
	actGrow       = 6
	actReproduce  = 7
	actToxin      = 8
	actDefense    = 9
	actMaxAction  = 9
)

// --- Data structures ---

type ParsedGene struct {
	Sense     int
	Condition int
	Action    int
	Weight    float64
}

type Cell struct {
	ID         uint64
	X, Y       float64
	VX, VY     float64 // velocity
	PrevX      float64
	PrevY      float64
	Heading    float64 // facing direction in radians
	Energy     float64
	MaxEnergy  float64
	Age        int
	Size       float64
	Defense    float64
	Genome     []int
	Genes      []ParsedGene
	ParentID   uint64
	Generation int
	ReproCool  int // ticks until can reproduce again
	Alive      bool
}

// --- Genome parsing ---

func parseGenome(raw []int) []ParsedGene {
	var genes []ParsedGene
	for i := 0; i < len(raw); i++ {
		if raw[i] == geneSTART {
			for j := i + 1; j < len(raw); j++ {
				if raw[j] == geneSTOP {
					if j-i >= 5 {
						sense := raw[i+1]
						cond := raw[i+2]
						action := raw[i+3]
						wRaw := raw[i+4]
						if wRaw < 1 {
							wRaw = 1
						}
						if wRaw > 13 {
							wRaw = 13
						}
						// Skip genes with invalid sense, condition, or action
						if sense >= senseLight && sense <= senseToxin &&
							cond >= condHigh && cond <= condAlways &&
							action >= actPhoto && action <= actMaxAction {
							genes = append(genes, ParsedGene{
								Sense:     sense,
								Condition: cond,
								Action:    action,
								Weight:    float64(wRaw) / 13.0,
							})
						}
					}
					i = j
					break
				}
			}
		}
	}
	return genes
}

func randomGenome(length int) []int {
	g := make([]int, length)
	for i := range g {
		g[i] = rand.Intn(16)
	}
	return g
}

func injectStarterGenes(g []int) []int {
	// Photosynthesis: sense light, always, photo, high weight
	photo := []int{0, senseLight, condAlways, actPhoto, 10, 15}
	// Reproduce: sense energy, high, reproduce, medium weight
	repro := []int{0, senseEnergy, condHigh, actReproduce, 8, 15}
	// Move forward: sense light, low, forward, medium weight (drift when dark)
	move := []int{0, senseLight, condLow, actForward, 6, 15}
	// Turn: sense oxygen, low, turn right (seek better area)
	turn := []int{0, senseOxygen, condLow, actTurnRight, 5, 15}

	result := make([]int, 0, len(g)+len(photo)+len(repro)+len(move)+len(turn))
	result = append(result, photo...)
	result = append(result, repro...)
	result = append(result, move...)
	result = append(result, turn...)
	result = append(result, g...)
	return result
}

func injectPredatorGenes(g []int) []int {
	// Eat: sense neighbors, high (many nearby), eat, high weight
	eat := []int{0, senseNeighbor, condHigh, actEat, 11, 15}
	// Move forward: always, forward (hunt)
	move := []int{0, senseDist, condLow, actForward, 10, 15}
	// Turn toward prey: sense neighbors, low (few nearby), turn to search
	turn := []int{0, senseNeighbor, condLow, actTurnRight, 6, 15}
	// Defense: sense neighbors, always, strengthen membrane
	def := []int{0, senseNeighbor, condAlways, actDefense, 7, 15}
	// Reproduce: sense energy, high, reproduce
	repro := []int{0, senseEnergy, condHigh, actReproduce, 8, 15}

	result := make([]int, 0, len(g)+len(eat)+len(move)+len(turn)+len(def)+len(repro))
	result = append(result, eat...)
	result = append(result, move...)
	result = append(result, turn...)
	result = append(result, def...)
	result = append(result, repro...)
	result = append(result, g...)
	return result
}

func mutateGenome(g []int) []int {
	out := make([]int, len(g))
	copy(out, g)

	// Point mutations
	for i := range out {
		if rand.Float64() < 0.05 {
			out[i] = rand.Intn(16)
		}
	}

	// Insertion
	if rand.Float64() < 0.02 {
		pos := rand.Intn(len(out) + 1)
		val := rand.Intn(16)
		n := make([]int, 0, len(out)+1)
		n = append(n, out[:pos]...)
		n = append(n, val)
		n = append(n, out[pos:]...)
		out = n
	}

	// Deletion
	if rand.Float64() < 0.02 && len(out) > 10 {
		pos := rand.Intn(len(out))
		out = append(out[:pos], out[pos+1:]...)
	}

	// Duplication
	if rand.Float64() < 0.01 && len(out) > 8 {
		segLen := 3 + rand.Intn(6)
		if segLen > len(out) {
			segLen = len(out)
		}
		start := rand.Intn(len(out) - segLen + 1)
		seg := make([]int, segLen)
		copy(seg, out[start:start+segLen])
		insertPos := rand.Intn(len(out) + 1)
		n := make([]int, 0, len(out)+segLen)
		n = append(n, out[:insertPos]...)
		n = append(n, seg...)
		n = append(n, out[insertPos:]...)
		out = n
	}

	return out
}

// --- Spatial hash (fixed grid, no map) ---

const (
	hashW = screenWidth/int(bucketSize) + 2
	hashH = screenHeight/int(bucketSize) + 2
)

type SpatialHash struct {
	buckets [hashW][hashH][]*Cell
	dirty   [][2]int // which buckets were written this tick
	scratch []*Cell  // reused query result buffer
}

func newSpatialHash() *SpatialHash {
	return &SpatialHash{}
}

func (s *SpatialHash) clear() {
	for _, k := range s.dirty {
		s.buckets[k[0]][k[1]] = s.buckets[k[0]][k[1]][:0]
	}
	s.dirty = s.dirty[:0]
}

func (s *SpatialHash) insert(c *Cell) {
	bx := int(c.X/bucketSize) % hashW
	by := int(c.Y / bucketSize)
	if bx < 0 {
		bx += hashW
	}
	if by < 0 || by >= hashH {
		return
	}
	if len(s.buckets[bx][by]) == 0 {
		s.dirty = append(s.dirty, [2]int{bx, by})
	}
	s.buckets[bx][by] = append(s.buckets[bx][by], c)
}

// query appends nearby cells into s.scratch and returns it.
func (s *SpatialHash) query(x, y, radius float64) []*Cell {
	s.scratch = s.scratch[:0]
	minBX := int((x - radius) / bucketSize)
	maxBX := int((x+radius)/bucketSize) + 1
	minBY := int((y - radius) / bucketSize)
	maxBY := int((y+radius)/bucketSize) + 1
	if minBY < 0 {
		minBY = 0
	}
	if maxBY >= hashH {
		maxBY = hashH - 1
	}
	for bx := minBX; bx <= maxBX; bx++ {
		wbx := bx % hashW
		if wbx < 0 {
			wbx += hashW
		}
		for by := minBY; by <= maxBY; by++ {
			for _, c := range s.buckets[wbx][by] {
				if c.Alive {
					s.scratch = append(s.scratch, c)
				}
			}
		}
	}
	return s.scratch
}

// --- Nutrient grid ---

type NutrientGrid struct {
	data [nutrientGridW][nutrientGridH]float64
	tmp  [nutrientGridW][nutrientGridH]float64
}

func (n *NutrientGrid) addAt(x, y, amount float64) {
	gx := int(x / nutrientCellW)
	gy := int(y / nutrientCellH)
	if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
		n.data[gx][gy] += amount
	}
}

func (n *NutrientGrid) getAt(x, y float64) float64 {
	gx := int(x / nutrientCellW)
	gy := int(y / nutrientCellH)
	if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
		return n.data[gx][gy]
	}
	return 0
}

func (n *NutrientGrid) absorbAt(x, y, rate float64) float64 {
	gx := int(x / nutrientCellW)
	gy := int(y / nutrientCellH)
	if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
		amount := n.data[gx][gy] * rate
		n.data[gx][gy] -= amount
		return amount
	}
	return 0
}

func (n *NutrientGrid) diffuse() {
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < nutrientGridH; y++ {
			sum := n.data[x][y]
			count := 1.0
			if x > 0 {
				sum += n.data[x-1][y]
				count++
			}
			if x < nutrientGridW-1 {
				sum += n.data[x+1][y]
				count++
			}
			if y > 0 {
				sum += n.data[x][y-1]
				count++
			}
			if y < nutrientGridH-1 {
				sum += n.data[x][y+1]
				count++
			}
			avg := sum / count
			n.tmp[x][y] = n.data[x][y] + (avg-n.data[x][y])*diffusionRate
			// Decay
			n.tmp[x][y] *= (1.0 - nutrientDecay)
		}
	}
	n.data = n.tmp

	// Nutrients sink downward (dead matter settles to the bottom)
	for x := 0; x < nutrientGridW; x++ {
		// Process bottom-up so sinking doesn't cascade in one tick
		for y := nutrientGridH - 2; y >= 0; y-- {
			sink := n.data[x][y] * nutrientSinkRate
			n.data[x][y] -= sink
			n.data[x][y+1] += sink
		}
	}
}

// --- Environment grid (reusable for oxygen, toxins) ---

type EnvGrid struct {
	data [nutrientGridW][nutrientGridH]float64
	tmp  [nutrientGridW][nutrientGridH]float64
}

func (g *EnvGrid) addAt(x, y, amount float64) {
	gx := int(x / nutrientCellW)
	gy := int(y / nutrientCellH)
	if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
		g.data[gx][gy] += amount
	}
}

func (g *EnvGrid) getAt(x, y float64) float64 {
	gx := int(x / nutrientCellW)
	gy := int(y / nutrientCellH)
	if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
		return g.data[gx][gy]
	}
	return 0
}

func (g *EnvGrid) diffuse(diffRate, decayRate float64) {
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < nutrientGridH; y++ {
			sum := g.data[x][y]
			count := 1.0
			if x > 0 {
				sum += g.data[x-1][y]
				count++
			}
			if x < nutrientGridW-1 {
				sum += g.data[x+1][y]
				count++
			}
			if y > 0 {
				sum += g.data[x][y-1]
				count++
			}
			if y < nutrientGridH-1 {
				sum += g.data[x][y+1]
				count++
			}
			avg := sum / count
			g.tmp[x][y] = g.data[x][y] + (avg-g.data[x][y])*diffRate
			g.tmp[x][y] *= (1.0 - decayRate)
			if g.tmp[x][y] < 0 {
				g.tmp[x][y] = 0
			}
		}
	}
	g.data = g.tmp
}

// --- Simulation ---

type Sim struct {
	cells      []*Cell
	hash       *SpatialHash
	nutrients  NutrientGrid
	oxygen     EnvGrid
	toxins     EnvGrid
	lightMap   [nutrientGridW][nutrientGridH]float64 // recomputed each tick
	nextID     uint64
	tick       int
	paused     bool
	speed      int // ticks per frame
}

func newSim() *Sim {
	s := &Sim{
		hash:       newSpatialHash(),
		nextID:     1,
		speed:      1,
	}
	s.seed()
	return s
}

func (s *Sim) seed() {
	s.cells = nil
	s.nextID = 1
	s.tick = 0
	s.nutrients = NutrientGrid{}
	s.oxygen = EnvGrid{}
	s.toxins = EnvGrid{}

	// Initialize oxygen: gradient from surface (top) to deep (bottom)
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < nutrientGridH; y++ {
			depth := float64(y) / float64(nutrientGridH)
			s.oxygen.data[x][y] = 5.0 * (1.0 - depth) // more oxygen near surface
		}
	}

	for i := 0; i < initialCells; i++ {
		genLen := 20 + rand.Intn(11)
		genome := randomGenome(genLen)
		if i < seededCells {
			genome = injectStarterGenes(genome)
		} else if i < seededCells+5 {
			// Seed 5 predator cells: eat, move toward, reproduce, defense
			genome = injectPredatorGenes(genome)
		}
		c := &Cell{
			ID:        s.nextID,
			X:         rand.Float64() * screenWidth,
			Y:         rand.Float64() * screenHeight * 0.6,
			Heading:   rand.Float64() * 2 * math.Pi,
			Energy:    30.0,
			MaxEnergy: 200.0,
			Size:      3.0,
			Genome:    genome,
			Genes:     parseGenome(genome),
			Alive:     true,
		}
		c.PrevX = c.X
		c.PrevY = c.Y
		s.nextID++
		s.cells = append(s.cells, c)
	}
}


// bottle wall bounds — cells live inside the glass
const bottleLeft = 12.0
const bottleRight = screenWidth - 12.0
const bottleTop = 3.0
const bottleBottom = screenHeight - 24.0

func wrapX(x float64) float64 {
	// Clamp to bottle walls (no wrapping — it's a closed bottle)
	if x < bottleLeft {
		return bottleLeft
	}
	if x >= bottleRight {
		return bottleRight - 1
	}
	return x
}

func clampY(y float64) float64 {
	if y < bottleTop {
		return bottleTop
	}
	if y >= bottleBottom {
		return bottleBottom - 1
	}
	return y
}


func distBetween(x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	return math.Sqrt(dx*dx + dy*dy)
}

func dirBetween(fromX, fromY, toX, toY float64) (float64, float64) {
	dx := toX - fromX
	dy := toY - fromY
	dist := math.Sqrt(dx*dx + dy*dy)
	if dist < 0.001 {
		return 0, 0
	}
	return dx / dist, dy / dist
}

func (s *Sim) senseValue(c *Cell, sense int, nearest *Cell, neighborCount int, nearestDist float64) float64 {
	switch sense {
	case senseLight:
		gx := int(c.X / nutrientCellW)
		gy := int(c.Y / nutrientCellH)
		if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
			return s.lightMap[gx][gy]
		}
		return 0
	case senseEnergy:
		return c.Energy / c.MaxEnergy
	case senseNeighbor:
		return math.Min(float64(neighborCount)/10.0, 1.0)
	case senseDist:
		if nearest == nil {
			return 1.0
		}
		return math.Min(nearestDist/200.0, 1.0)
	case senseNutrient:
		return math.Min(s.nutrients.getAt(c.X, c.Y)/10.0, 1.0)
	case senseAge:
		return float64(c.Age) / float64(maxAge)
	case senseSize:
		return c.Size / maxSize
	case senseOxygen:
		return math.Min(s.oxygen.getAt(c.X, c.Y)/5.0, 1.0)
	case senseToxin:
		return math.Min(s.toxins.getAt(c.X, c.Y)/5.0, 1.0)
	}
	return 0
}

func checkCondition(val float64, cond int) bool {
	switch cond {
	case condHigh:
		return val > 0.6
	case condLow:
		return val < 0.4
	case condMedium:
		return val >= 0.4 && val <= 0.6
	case condAlways:
		return true
	}
	return false
}

func (s *Sim) doTick() {
	// Rebuild spatial hash
	s.hash.clear()
	for _, c := range s.cells {
		if c.Alive {
			s.hash.insert(c)
		}
	}

	// Count cells per grid tile for crowding oxygen drain
	var crowding [nutrientGridW][nutrientGridH]int
	for _, c := range s.cells {
		if c.Alive {
			gx := int(c.X / nutrientCellW)
			gy := int(c.Y / nutrientCellH)
			if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
				crowding[gx][gy]++
			}
		}
	}

	// Build light map — sunlight is absorbed as it passes through cells top-down
	for x := 0; x < nutrientGridW; x++ {
		light := 1.0 // full sunlight at the top
		for y := 0; y < nutrientGridH; y++ {
			// Base attenuation by depth
			depthLight := math.Max(0, 1.0-float64(y)/float64(nutrientGridH))
			s.lightMap[x][y] = depthLight * light
			// Cells in this tile absorb some light — each cell blocks a fraction
			if crowding[x][y] > 0 {
				light *= math.Pow(0.92, float64(crowding[x][y])) // 8% absorbed per cell
			}
		}
	}

	// Collect new children to add after tick
	newCells := make([]*Cell, 0, 32)
	livingCount := 0

	for _, c := range s.cells {
		if !c.Alive {
			continue
		}
		livingCount++
		c.PrevX = c.X
		c.PrevY = c.Y

		// Find neighbors
		searchRadius := math.Max(c.Size*3, 50.0)
		nearby := s.hash.query(c.X, c.Y, searchRadius)

		var nearest *Cell
		nearestDist := math.MaxFloat64
		neighborCount := 0
		for _, n := range nearby {
			if n.ID == c.ID || !n.Alive {
				continue
			}
			d := distBetween(c.X, c.Y, n.X, n.Y)
			if d < searchRadius {
				neighborCount++
				if d < nearestDist {
					nearestDist = d
					nearest = n
				}
			}
		}

		// Accumulate actions — fixed array, no allocation (actions 1–10)
		var actions [actMaxAction + 1]float64
		for _, g := range c.Genes {
			if g.Action >= 1 && g.Action <= actMaxAction {
				val := s.senseValue(c, g.Sense, nearest, neighborCount, nearestDist)
				if checkCondition(val, g.Condition) {
					actions[g.Action] += g.Weight
				}
			}
		}

		// accel scales force by 1/size: big cells accelerate slowly
		accel := 0.4 / c.Size

		// Apply actions (array index, no allocation)
		if w := actions[actPhoto]; w > 0 {
			// Use light map — accounts for shadowing by cells above
			clx := int(c.X / nutrientCellW)
			cly := int(c.Y / nutrientCellH)
			light := 0.0
			if clx >= 0 && clx < nutrientGridW && cly >= 0 && cly < nutrientGridH {
				light = s.lightMap[clx][cly]
			}
			photoOutput := light * w * c.Size
			c.Energy += photoOutput
			// Photosynthesis produces oxygen
			s.oxygen.addAt(c.X, c.Y, photoOutput*oxygenPhotoRate)
		}

		if w := actions[actForward]; w > 0 {
			c.VX += math.Cos(c.Heading) * w * accel
			c.VY += math.Sin(c.Heading) * w * accel
		}

		if w := actions[actTurnLeft]; w > 0 {
			c.Heading -= 0.2 * w // ~11 degrees per unit weight
		}

		if w := actions[actTurnRight]; w > 0 {
			c.Heading += 0.2 * w
		}

		if w := actions[actEat]; w > 0 && nearest != nil {
			d := distBetween(c.X, c.Y, nearest.X, nearest.Y)
			if d < c.Size+nearest.Size {
				if w > nearest.Defense {
					stolen := nearest.Energy * 0.5 * w
					c.Energy += stolen
					nearest.Energy -= stolen
				} else {
					c.Energy -= 1.0
				}
			}
		}

		if w := actions[actGrow]; w > 0 {
			c.Size += 0.01 * w
			if c.Size > maxSize {
				c.Size = maxSize
			}
		}

		if w := actions[actReproduce]; w > 0 {
			if c.ReproCool <= 0 && c.Energy > reproThreshold*c.Size && livingCount+len(newCells) < maxCells {
				childEnergy := c.Energy * 0.5
				c.Energy -= childEnergy
				c.ReproCool = 100 // cooldown: ~5 seconds at 20 tps
				childGenome := mutateGenome(c.Genome)
				child := &Cell{
					ID:         s.nextID,
					X:          wrapX(c.X + (rand.Float64()-0.5)*c.Size),
					Y:          clampY(c.Y + (rand.Float64()-0.5)*c.Size),
					Heading:    rand.Float64() * 2 * math.Pi,
					Energy:     childEnergy,
					MaxEnergy:  200.0,
					Size:       math.Max(minSize, c.Size*0.8),
					Genome:     childGenome,
					Genes:      parseGenome(childGenome),
					ParentID:   c.ID,
					Generation: c.Generation + 1,
					Alive:      true,
				}
				child.PrevX = child.X
				child.PrevY = child.Y
				s.nextID++
				newCells = append(newCells, child)
			}
		}

		if w := actions[actToxin]; w > 0 {
			// Deposit toxins into the environment grid
			s.toxins.addAt(c.X, c.Y, w*c.Size*0.5)
			c.Energy -= 0.5 * w
		}

		if w := actions[actDefense]; w > 0 {
			c.Defense += 0.01 * w
			if c.Defense > maxDefense {
				c.Defense = maxDefense
			}
		}

		// Separation — push overlapping cells apart (physical, not gene-driven)
		for _, n := range nearby {
			if n.ID == c.ID || !n.Alive {
				continue
			}
			d := distBetween(c.X, c.Y, n.X, n.Y)
			minDist := c.Size + n.Size
			if d < minDist && d > 0.001 {
				overlap := minDist - d
				dx, dy := dirBetween(c.X, c.Y, n.X, n.Y)
				// Force proportional to overlap, split by mass (size)
				totalSize := c.Size + n.Size
				cShare := n.Size / totalSize // bigger neighbour pushes c more
				force := overlap * 0.25 * cShare
				c.VX -= dx * force
				c.VY -= dy * force
			}
		}

		// Passive drift — tiny random nudge into velocity
		driftForce := 0.05 / c.Size
		c.VX += (rand.Float64()-0.5) * driftForce * 2
		c.VY += (rand.Float64()-0.5) * driftForce * 2

		// Boundary push: repel from bottle walls
		const edgeMargin = 30.0
		if c.Y < bottleTop+edgeMargin {
			c.VY += (bottleTop + edgeMargin - c.Y) * 0.02
		}
		if c.Y > bottleBottom-edgeMargin {
			c.VY -= (c.Y - (bottleBottom - edgeMargin)) * 0.02
		}
		if c.X < bottleLeft+edgeMargin {
			c.VX += (bottleLeft + edgeMargin - c.X) * 0.02
		}
		if c.X > bottleRight-edgeMargin {
			c.VX -= (c.X - (bottleRight - edgeMargin)) * 0.02
		}

		// Drag — larger cells have more drag (they're slower overall)
		drag := 1.0 - (0.12 + c.Size*0.008)
		c.VX *= drag
		c.VY *= drag

		// Cap speed
		spd := math.Sqrt(c.VX*c.VX + c.VY*c.VY)
		maxSpd := maxSpeed / c.Size
		if spd > maxSpd {
			scale := maxSpd / spd
			c.VX *= scale
			c.VY *= scale
		}

		// Integrate position
		c.X = wrapX(c.X + c.VX)
		c.Y = clampY(c.Y + c.VY)

		// Defense decay
		c.Defense -= defenseDecay
		if c.Defense < 0 {
			c.Defense = 0
		}

		// Nutrient absorption
		c.Energy += s.nutrients.absorbAt(c.X, c.Y, 0.1)

		// Oxygen consumption — scales with crowding
		localO2 := s.oxygen.getAt(c.X, c.Y)
		gx := int(c.X / nutrientCellW)
		gy := int(c.Y / nutrientCellH)
		crowd := 1.0
		if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
			crowd = math.Max(1.0, float64(crowding[gx][gy]))
		}
		// More cells on the same tile = each cell needs more oxygen (competition)
		crowdFactor := 1.0 + (crowd-1)*0.3
		breathNeeded := oxygenBreathRate * c.Size * crowdFactor
		if localO2 >= breathNeeded {
			s.oxygen.addAt(c.X, c.Y, -breathNeeded)
		} else {
			// Not enough oxygen — consume what's there, pay energy penalty
			s.oxygen.addAt(c.X, c.Y, -localO2)
			deficit := 1.0 - localO2/breathNeeded // 0..1
			c.Energy -= lowOxygenPenalty * deficit * c.Size
		}

		// Toxin damage — local toxins hurt cells
		localToxin := s.toxins.getAt(c.X, c.Y)
		if localToxin > 0 {
			dmg := localToxin * toxinDamage
			// Defense reduces toxin damage
			dmg *= (1.0 - c.Defense*0.5)
			c.Energy -= dmg
		}

		// Maintenance cost (deeper = higher pressure cost)
		depth := c.Y / screenHeight // 0 at top, 1 at bottom
		c.Energy -= 0.3 + (c.Size * 0.1) + (float64(len(c.Genes)) * 0.05) + (depth * depthCostMax)

		// Cap energy
		if c.Energy > c.MaxEnergy {
			c.Energy = c.MaxEnergy
		}

		// Aging & cooldowns
		c.Age++
		if c.ReproCool > 0 {
			c.ReproCool--
		}

		// Death
		if c.Energy <= 0 || c.Age > maxAge {
			c.Alive = false
			s.nutrients.addAt(c.X, c.Y, c.Size*2)
		}
	}

	// Add new cells
	s.cells = append(s.cells, newCells...)

	// Compact dead cells periodically
	if s.tick%30 == 0 {
		alive := make([]*Cell, 0, len(s.cells))
		for _, c := range s.cells {
			if c.Alive {
				alive = append(alive, c)
			}
		}
		s.cells = alive
	}

	// Nutrient diffusion
	s.nutrients.diffuse()

	// Oxygen: ambient production at surface + diffusion
	for x := 0; x < nutrientGridW; x++ {
		// Top rows get ambient oxygen (simulates air-water interface)
		for y := 0; y < 10; y++ {
			s.oxygen.data[x][y] += oxygenSurfaceAdd * (1.0 - float64(y)/10.0)
		}
		// Cap oxygen levels
		for y := 0; y < nutrientGridH; y++ {
			if s.oxygen.data[x][y] > 10.0 {
				s.oxygen.data[x][y] = 10.0
			}
		}
	}
	s.oxygen.diffuse(oxygenDiffusion, oxygenDecay)

	// Toxin diffusion and decay
	s.toxins.diffuse(toxinDiffusion, toxinDecay)

	s.tick++
}

// --- Rendering ---

func cellColor(c *Cell) (r, g, b uint8) {
	var photoW, eatW, toxinW float64
	var photoSenseSum int

	for _, gene := range c.Genes {
		switch gene.Action {
		case actPhoto:
			photoW += gene.Weight
			photoSenseSum += gene.Sense
		case actEat:
			eatW += gene.Weight
		case actToxin:
			toxinW += gene.Weight
		}
	}

	var fr, fg, fb float64

	if photoW > 0 {
		// Green-based, shift by sense
		avgSense := float64(photoSenseSum) / math.Max(1, float64(countAction(c.Genes, actPhoto)))
		switch {
		case avgSense < 2: // light-sensing → pure green
			fr, fg, fb = 0.2, 0.9, 0.2
		case avgSense < 3: // energy-sensing → yellow-green
			fr, fg, fb = 0.6, 0.9, 0.1
		default: // neighbor-sensing → teal
			fr, fg, fb = 0.1, 0.8, 0.7
		}
		sat := math.Min(photoW, 1.0)
		fr = 0.5 + (fr-0.5)*sat
		fg = 0.5 + (fg-0.5)*sat
		fb = 0.5 + (fb-0.5)*sat
	} else if eatW > 0 {
		sat := math.Min(eatW, 1.0)
		fr = 0.5 + 0.5*sat
		fg = 0.5 - 0.2*sat
		fb = 0.5 - 0.3*sat
	} else {
		fr, fg, fb = 0.5, 0.5, 0.5
	}

	// Toxin purple tint
	if toxinW > 0 {
		t := math.Min(toxinW*0.5, 0.4)
		fr = fr*(1-t) + 0.7*t
		fg = fg*(1-t) + 0.2*t
		fb = fb*(1-t) + 0.9*t
	}

	return uint8(fr * 255), uint8(fg * 255), uint8(fb * 255)
}

func countAction(genes []ParsedGene, action int) int {
	n := 0
	for _, g := range genes {
		if g.Action == action {
			n++
		}
	}
	return n
}

// --- Save / Load ---

type SaveCell struct {
	ID         uint64    `json:"id"`
	X          float64   `json:"x"`
	Y          float64   `json:"y"`
	VX         float64   `json:"vx"`
	VY         float64   `json:"vy"`
	Heading    float64   `json:"heading"`
	Energy     float64   `json:"energy"`
	MaxEnergy  float64   `json:"max_energy"`
	Age        int       `json:"age"`
	Size       float64   `json:"size"`
	Defense    float64   `json:"defense"`
	Genome     []int     `json:"genome"`
	ParentID   uint64    `json:"parent_id"`
	Generation int       `json:"generation"`
}

type SaveData struct {
	Tick      int                                    `json:"tick"`
	NextID    uint64                                 `json:"next_id"`
	Cells     []SaveCell                             `json:"cells"`
	Nutrients [nutrientGridW][nutrientGridH]float64   `json:"nutrients"`
	Oxygen    [nutrientGridW][nutrientGridH]float64   `json:"oxygen"`
	Toxins    [nutrientGridW][nutrientGridH]float64   `json:"toxins"`
}

func (s *Sim) export(path string) error {
	sd := SaveData{
		Tick:      s.tick,
		NextID:    s.nextID,
		Nutrients: s.nutrients.data,
		Oxygen:    s.oxygen.data,
		Toxins:    s.toxins.data,
	}
	for _, c := range s.cells {
		if !c.Alive {
			continue
		}
		sd.Cells = append(sd.Cells, SaveCell{
			ID: c.ID, X: c.X, Y: c.Y, VX: c.VX, VY: c.VY,
			Heading: c.Heading, Energy: c.Energy, MaxEnergy: c.MaxEnergy,
			Age: c.Age, Size: c.Size, Defense: c.Defense,
			Genome: c.Genome, ParentID: c.ParentID, Generation: c.Generation,
		})
	}
	data, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (s *Sim) importFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sd SaveData
	if err := json.Unmarshal(data, &sd); err != nil {
		return err
	}
	s.tick = sd.Tick
	s.nextID = sd.NextID
	s.nutrients.data = sd.Nutrients
	s.oxygen.data = sd.Oxygen
	s.toxins.data = sd.Toxins
	s.cells = make([]*Cell, 0, len(sd.Cells))
	for _, sc := range sd.Cells {
		c := &Cell{
			ID: sc.ID, X: sc.X, Y: sc.Y, VX: sc.VX, VY: sc.VY,
			Heading: sc.Heading, Energy: sc.Energy, MaxEnergy: sc.MaxEnergy,
			Age: sc.Age, Size: sc.Size, Defense: sc.Defense,
			Genome: sc.Genome, Genes: parseGenome(sc.Genome),
			ParentID: sc.ParentID, Generation: sc.Generation, Alive: true,
		}
		c.PrevX = c.X
		c.PrevY = c.Y
		s.cells = append(s.cells, c)
	}
	return nil
}

// --- Game struct for Ebitengine ---

// simTickRates maps speed index → sim ticks per second
var simTickRates = []int{20, 40, 100, 200}
var simTickLabels = []string{"1x", "2x", "5x", "10x"}

// button is a clickable screen-space rectangle.
type button struct {
	x, y, w, h int
	label       string
}

func (b button) contains(mx, my int) bool {
	return mx >= b.x && mx < b.x+b.w && my >= b.y && my < b.y+b.h
}

func drawButton(screen *ebiten.Image, b button, active bool) {
	bg := color.RGBA{0x22, 0x2a, 0x44, 220}
	if active {
		bg = color.RGBA{0x33, 0x88, 0xff, 220}
	}
	vector.FillRect(screen, float32(b.x), float32(b.y), float32(b.w), float32(b.h), bg, false)
	// border
	border := color.RGBA{0x55, 0x77, 0xcc, 255}
	vector.FillRect(screen, float32(b.x), float32(b.y), float32(b.w), 1, border, false)
	vector.FillRect(screen, float32(b.x), float32(b.y+b.h-1), float32(b.w), 1, border, false)
	vector.FillRect(screen, float32(b.x), float32(b.y), 1, float32(b.h), border, false)
	vector.FillRect(screen, float32(b.x+b.w-1), float32(b.y), 1, float32(b.h), border, false)
	// label centered (ebitenutil uses 6×13 px chars)
	charW := 6
	tx := b.x + (b.w-len(b.label)*charW)/2
	ty := b.y + (b.h-13)/2
	ebitenutil.DebugPrintAt(screen, b.label, tx, ty)
}

type Game struct {
	sim          *Sim
	clickedCell  *Cell
	camX, camY   float64
	zoom         float64
	dragging     bool
	dragLastX    int
	dragLastY    int
	hudCells     int
	hudMaxGen    int
	speedIdx     int
	tickAccum    float64
	speedButtons  []button
	actionButtons []button // export, import
	statusMsg     string   // brief feedback message
	statusTick    int      // tick when status was set
}

func newGame() *Game {
	return &Game{
		sim:           newSim(),
		camX:          screenWidth / 2,
		camY:          screenHeight / 2,
		zoom:          1.0,
		speedButtons:  make([]button, 0, len(simTickLabels)),
		actionButtons: make([]button, 0, 2),
	}
}

// worldToScreen converts a world coordinate to a screen pixel position.
func (g *Game) worldToScreen(wx, wy float64) (float32, float32) {
	sx := (wx-g.camX)*g.zoom + screenWidth/2
	sy := (wy-g.camY)*g.zoom + screenHeight/2
	return float32(sx), float32(sy)
}

// screenToWorld converts a screen pixel position to a world coordinate.
func (g *Game) screenToWorld(sx, sy int) (float64, float64) {
	wx := (float64(sx)-screenWidth/2)/g.zoom + g.camX
	wy := (float64(sy)-screenHeight/2)/g.zoom + g.camY
	return wx, wy
}

func (g *Game) Update() error {
	// Controls
	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}

	if inputJustPressed(ebiten.KeySpace) {
		g.sim.paused = !g.sim.paused
	}

	if inputJustPressed(ebiten.KeyR) {
		g.sim = newSim()
		g.camX = screenWidth / 2
		g.camY = screenHeight / 2
		g.zoom = 1.0
		g.clickedCell = nil
		g.speedIdx = 0
		g.tickAccum = 0
		return nil
	}



	// Scroll wheel zoom — centered on mouse cursor
	_, scrollY := ebiten.Wheel()
	if scrollY != 0 {
		mx, my := ebiten.CursorPosition()
		// World point under mouse before zoom
		wx, wy := g.screenToWorld(mx, my)
		factor := math.Pow(1.12, scrollY)
		g.zoom *= factor
		if g.zoom < 0.15 {
			g.zoom = 0.15
		}
		if g.zoom > 12.0 {
			g.zoom = 12.0
		}
		// Adjust camera so the world point under mouse stays fixed
		g.camX = wx - (float64(mx)-screenWidth/2)/g.zoom
		g.camY = wy - (float64(my)-screenHeight/2)/g.zoom
	}

	// Right-click drag to pan
	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) {
		mx, my := ebiten.CursorPosition()
		if g.dragging {
			dx := float64(mx-g.dragLastX) / g.zoom
			dy := float64(my-g.dragLastY) / g.zoom
			g.camX -= dx
			g.camY -= dy
		}
		g.dragging = true
		g.dragLastX = mx
		g.dragLastY = my
	} else {
		g.dragging = false
	}

	// Left click: buttons take priority, then cell inspect
	if mouseJustPressed(ebiten.MouseButtonLeft) {
		mx, my := ebiten.CursorPosition()
		hitButton := false
		for i, btn := range g.speedButtons {
			if btn.contains(mx, my) {
				g.speedIdx = i
				hitButton = true
				break
			}
		}
		if !hitButton {
			for _, btn := range g.actionButtons {
				if btn.contains(mx, my) {
					hitButton = true
					switch btn.label {
					case "Export":
						path := fmt.Sprintf("cellarium_save_%d.json", time.Now().Unix())
						if err := g.sim.export(path); err != nil {
							g.statusMsg = fmt.Sprintf("Export failed: %v", err)
						} else {
							g.statusMsg = fmt.Sprintf("Saved %s", path)
						}
						g.statusTick = g.sim.tick
					case "Import":
						entries, _ := os.ReadDir(".")
						var latest string
						for _, e := range entries {
							name := e.Name()
							if len(name) > 15 && name[:15] == "cellarium_save_" && name[len(name)-5:] == ".json" {
								latest = name
							}
						}
						if latest != "" {
							if err := g.sim.importFile(latest); err != nil {
								g.statusMsg = fmt.Sprintf("Import failed: %v", err)
							} else {
								g.statusMsg = fmt.Sprintf("Loaded %s (%d cells)", latest, len(g.sim.cells))
								g.clickedCell = nil
							}
						} else {
							g.statusMsg = "No save file found"
						}
						g.statusTick = g.sim.tick
					}
					break
				}
			}
		}
		if !hitButton {
			wx, wy := g.screenToWorld(mx, my)
			best := (*Cell)(nil)
			bestDist := math.MaxFloat64
			for _, c := range g.sim.cells {
				if !c.Alive {
					continue
				}
				d := distBetween(wx, wy, c.X, c.Y)
				if d < (c.Size+5)/g.zoom && d < bestDist {
					bestDist = d
					best = c
				}
			}
			if best != nil {
				g.clickedCell = best
			} else {
				g.clickedCell = nil
			}
		}
	}

	if !g.sim.paused {
		// Accumulate fractional ticks based on sim rate and render TPS
		ticksPerSec := float64(simTickRates[g.speedIdx])
		renderTPS := float64(ebiten.TPS())
		if renderTPS <= 0 {
			renderTPS = 60
		}
		g.tickAccum += ticksPerSec / renderTPS
		ticks := int(g.tickAccum)
		g.tickAccum -= float64(ticks)
		// Cap to avoid spiral of death if sim is slow
		if ticks > 20 {
			ticks = 20
		}
		for range ticks {
			g.sim.doTick()
		}
		if ticks > 0 {
			living, maxGen := 0, 0
			for _, c := range g.sim.cells {
				if c.Alive {
					living++
					if c.Generation > maxGen {
						maxGen = c.Generation
					}
				}
			}
			g.hudCells = living
			g.hudMaxGen = maxGen
		}
	}

	return nil
}

var keyStates   = make(map[ebiten.Key]bool)
var mouseStates = make(map[ebiten.MouseButton]bool)

func inputJustPressed(key ebiten.Key) bool {
	pressed := ebiten.IsKeyPressed(key)
	was := keyStates[key]
	keyStates[key] = pressed
	return pressed && !was
}

func mouseJustPressed(btn ebiten.MouseButton) bool {
	pressed := ebiten.IsMouseButtonPressed(btn)
	was := mouseStates[btn]
	mouseStates[btn] = pressed
	return pressed && !was
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Background
	screen.Fill(color.RGBA{0x08, 0x0c, 0x18, 0xff})

	// Draw environment grid — light, oxygen, toxin per tile
	z := float32(g.zoom)
	tileSW := float32(nutrientCellW) * z
	tileSH := float32(nutrientCellH) * z
	for gx := 0; gx < nutrientGridW; gx++ {
		for gy := 0; gy < nutrientGridH; gy++ {
			sx, sy := g.worldToScreen(float64(gx)*nutrientCellW, float64(gy)*nutrientCellH)

			// Cull off-screen tiles
			if sx+tileSW < 0 || sx > screenWidth || sy+tileSH < 0 || sy > screenHeight {
				continue
			}

			// Light: shadow overlay where sunlight is blocked
			light := g.sim.lightMap[gx][gy]
			shadow := 1.0 - light
			if shadow > 0.01 {
				a := uint8(shadow * 60)
				vector.FillRect(screen, sx, sy, tileSW, tileSH,
					color.RGBA{0x00, 0x00, 0x10, a}, false)
			}

			// Oxygen: subtle blue tint
			o2 := g.sim.oxygen.data[gx][gy]
			if o2 > 0.1 {
				a := uint8(math.Min(float64(o2)/5.0, 1.0) * 14)
				vector.FillRect(screen, sx, sy, tileSW, tileSH,
					color.RGBA{0x30, 0x70, 0xd0, a}, false)
			}

			// Toxin: purple-red tint
			tox := g.sim.toxins.data[gx][gy]
			if tox > 0.1 {
				a := uint8(math.Min(float64(tox)/5.0, 1.0) * 25)
				vector.FillRect(screen, sx, sy, tileSW, tileSH,
					color.RGBA{0xa0, 0x20, 0x80, a}, false)
			}
		}
	}

	// Draw cells
	for _, c := range g.sim.cells {
		if !c.Alive {
			continue
		}

		sx, sy := g.worldToScreen(c.X, c.Y)
		z := float32(g.zoom)

		r, gb, b := cellColor(c)
		fillColor := color.RGBA{r, gb, b, 0xff}

		// Cull cells outside screen
		if sx < -50 || sx > screenWidth+50 || sy < -50 || sy > screenHeight+50 {
			continue
		}

		// Movement trail
		dx := c.X - c.PrevX
		dy := c.Y - c.PrevY
		if dx*dx+dy*dy > 1.0 {
			trailColor := color.RGBA{r, gb, b, 50}
			px, py := g.worldToScreen(c.PrevX, c.PrevY)
			vector.FillCircle(screen, px, py, float32(math.Max(1, c.Size*0.5))*z, trailColor, false)
		}

		// Border
		borderThickness := 1.0 + c.Defense*3.0
		borderR := uint8(math.Min(255, float64(r)+40))
		borderG := uint8(math.Min(255, float64(gb)+40))
		borderB := uint8(math.Min(255, float64(b)+40))
		borderColor := color.RGBA{borderR, borderG, borderB, 0xff}

		// Draw border circle then fill
		vector.FillCircle(screen, sx, sy, float32(c.Size+borderThickness)*z, borderColor, false)
		vector.FillCircle(screen, sx, sy, float32(c.Size)*z, fillColor, false)

		// Internal details (nucleus dots)
		geneCount := len(c.Genes)
		if geneCount >= 5 && c.Size > 3 {
			dotColor := color.RGBA{borderR, borderG, borderB, 200}
			vector.FillCircle(screen, sx, sy, float32(math.Max(1, c.Size*0.2))*z, dotColor, false)
		}
		if geneCount >= 8 && c.Size > 4 {
			dotColor := color.RGBA{borderR, borderG, borderB, 160}
			ox, oy := g.worldToScreen(c.X+c.Size*0.3, c.Y-c.Size*0.3)
			vector.FillCircle(screen, ox, oy, float32(math.Max(1, c.Size*0.15))*z, dotColor, false)
		}
	}

	// Bottle borders — clean lines in world space
	borderCol := color.RGBA{0x40, 0x60, 0x80, 255}
	bz := float32(g.zoom)

	tlX, tlY := g.worldToScreen(bottleLeft, bottleTop)
	brX, brY := g.worldToScreen(bottleRight, bottleBottom)
	bw := brX - tlX
	bh := brY - tlY

	// Left wall
	vector.FillRect(screen, tlX-2*bz, tlY, 2*bz, bh, borderCol, false)
	// Right wall
	vector.FillRect(screen, brX, tlY, 2*bz, bh, borderCol, false)
	// Bottom
	vector.FillRect(screen, tlX-2*bz, brY, bw+4*bz, 2*bz, borderCol, false)
	// Top rim — dashed to indicate open to air
	dashW := 4 * bz
	dashGap := 8 * bz
	for x := tlX; x < brX; x += dashGap {
		vector.FillRect(screen, x, tlY-1*bz, dashW, 1*bz, color.RGBA{0x50, 0x80, 0xb0, 180}, false)
	}

	// HUD text
	pausedStr := ""
	if g.sim.paused {
		pausedStr = "  [PAUSED]"
	}
	// Compute average oxygen and toxin levels
	var totalO2, totalTox float64
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < nutrientGridH; y++ {
			totalO2 += g.sim.oxygen.data[x][y]
			totalTox += g.sim.toxins.data[x][y]
		}
	}
	gridCells := float64(nutrientGridW * nutrientGridH)
	avgO2 := totalO2 / gridCells
	avgTox := totalTox / gridCells
	hud := fmt.Sprintf("Cells: %d   Gen: %d   Tick: %d   O2: %.2f   Tox: %.2f   Zoom: %.1fx%s",
		g.hudCells, g.hudMaxGen, g.sim.tick, avgO2, avgTox, g.zoom, pausedStr)
	ebitenutil.DebugPrintAt(screen, hud, 8, 8)

	// Speed buttons
	const btnW, btnH, btnPad = 38, 20, 4
	startX := 8
	startY := 26
	g.speedButtons = g.speedButtons[:0]
	for i, label := range simTickLabels {
		btn := button{
			x:     startX + i*(btnW+btnPad),
			y:     startY,
			w:     btnW,
			h:     btnH,
			label: label,
		}
		g.speedButtons = append(g.speedButtons, btn)
		drawButton(screen, btn, i == g.speedIdx)
	}

	// Action buttons (Export, Import) — right of speed buttons
	actionStartX := startX + len(simTickLabels)*(btnW+btnPad) + 12
	g.actionButtons = g.actionButtons[:0]
	for i, label := range []string{"Export", "Import"} {
		abtnW := 52
		btn := button{
			x:     actionStartX + i*(abtnW+btnPad),
			y:     startY,
			w:     abtnW,
			h:     btnH,
			label: label,
		}
		g.actionButtons = append(g.actionButtons, btn)
		drawButton(screen, btn, false)
	}

	// Status message (fades after a few seconds)
	if g.statusMsg != "" && g.sim.tick-g.statusTick < 200 {
		ebitenutil.DebugPrintAt(screen, g.statusMsg, 8, startY+btnH+6)
	}

	// Cell inspector popup
	if g.clickedCell != nil {
		c := g.clickedCell
		if !c.Alive {
			g.clickedCell = nil
		} else {
			drawCellPopup(screen, c)
		}
	}
}

func drawCellPopup(screen *ebiten.Image, c *Cell) {
	const (
		popW    = 300
		popX    = screenWidth - popW - 10
		popY    = 10
		lineH   = 14
		padding = 8
	)

	senseNames := map[int]string{1: "light", 2: "energy", 3: "neighbors", 4: "dist", 5: "nutrient", 6: "age", 7: "size", 8: "oxygen", 9: "toxin"}
	condNames  := map[int]string{1: "HIGH", 2: "LOW", 3: "MED", 4: "ALWAYS"}
	actNames   := map[int]string{1: "photo", 2: "forward", 3: "turnLeft", 4: "turnRight", 5: "eat", 6: "grow", 7: "reproduce", 8: "toxin", 9: "defense"}

	lines := []string{
		fmt.Sprintf("Cell #%d  (gen %d)", c.ID, c.Generation),
		fmt.Sprintf("Age:     %d / %d", c.Age, maxAge),
		fmt.Sprintf("Energy:  %.1f / %.1f", c.Energy, c.MaxEnergy),
		fmt.Sprintf("Size:    %.1f", c.Size),
		fmt.Sprintf("Defense: %.2f", c.Defense),
		fmt.Sprintf("Genes:   %d  (genome len %d)", len(c.Genes), len(c.Genome)),
		"",
	}
	for i, g := range c.Genes {
		sn := senseNames[g.Sense]
		if sn == "" { sn = fmt.Sprintf("?%d", g.Sense) }
		cn := condNames[g.Condition]
		if cn == "" { cn = fmt.Sprintf("?%d", g.Condition) }
		an := actNames[g.Action]
		if an == "" { an = fmt.Sprintf("?%d", g.Action) }
		lines = append(lines, fmt.Sprintf("[%d] %s if %s → %s (%.2f)", i+1, sn, cn, an, g.Weight))
	}
	lines = append(lines, "", "[click elsewhere to close]")

	popH := padding*2 + len(lines)*lineH

	// Background panel
	vector.FillRect(screen,
		float32(popX-padding), float32(popY-padding),
		float32(popW+padding*2), float32(popH),
		color.RGBA{0x10, 0x14, 0x28, 220}, false)

	// Colored title bar using cell's own color
	cr, cg, cb := cellColor(c)
	vector.FillRect(screen,
		float32(popX-padding), float32(popY-padding),
		float32(popW+padding*2), float32(lineH+padding),
		color.RGBA{cr, cg, cb, 180}, false)

	// Border
	borderCol := color.RGBA{cr, cg, cb, 255}
	// top
	vector.FillRect(screen, float32(popX-padding), float32(popY-padding), float32(popW+padding*2), 1, borderCol, false)
	// bottom
	vector.FillRect(screen, float32(popX-padding), float32(popY-padding+popH), float32(popW+padding*2), 1, borderCol, false)
	// left
	vector.FillRect(screen, float32(popX-padding), float32(popY-padding), 1, float32(popH), borderCol, false)
	// right
	vector.FillRect(screen, float32(popX-padding+popW+padding*2), float32(popY-padding), 1, float32(popH), borderCol, false)

	// Text
	for i, line := range lines {
		ebitenutil.DebugPrintAt(screen, line, popX, popY+i*lineH)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Cellarium")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	game := newGame()

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
