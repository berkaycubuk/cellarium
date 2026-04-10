package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"os"
	"sync"
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

	maxCells         = 2000
	initialCells     = 50
	seededCells      = 20
	corpseDecayPerSize = 500 // ticks of decay per unit of cell size (size 2 → 1000 ticks, size 15 → 7500 ticks)
	maxSize        = 15.0
	minSize        = 2.0
	maxSpeed       = 3.0
	maxDefense       = 1.0
	defenseDecay     = 0.008  // faster passive decay — armor crumbles without upkeep
	defenseCostRate  = 0.15   // energy per tick per unit of defense (armor is expensive)
	defenseDragMul   = 0.4    // extra drag fraction at max defense (0.4 = 40% slower)
	defensePhotoMul  = 0.3    // photosynthesis reduction at max defense (thick shell blocks light)
	reproThreshold = 50.0
	diffusionRate  = 0.10
	nutrientDecay  = 0.01

	// Oxygen
	oxygenDiffusion  = 0.06   // oxygen mixes slowly — local depletion matters
	oxygenDecay      = 0.0002 // very slow natural loss; cell consumption is the real drain
	oxygenSurfaceAdd = 0.04   // ambient oxygen added at surface each tick
	oxygenPhotoRate  = 0.1    // oxygen produced per unit of photosynthesis
	oxygenBreathRate = 0.05   // oxygen consumed per cell per tick (base)
	lowOxygenPenalty = 0.5    // energy cost when local oxygen is near zero

	// Growth
	growEnergyCost   = 0.8  // energy per 0.01 size gained (growing is expensive)
	starveShrinkRate = 0.002 // size lost per tick when energy < 10% of max

	// Toxins
	toxinDiffusion    = 0.03  // toxins spread slowly
	toxinDecay        = 0.02  // toxins break down over time
	toxinDamage       = 0.3   // energy damage per tick per unit of local toxin
	toxicityBuildRate = 0.5   // environmental toxin → internal toxicity per tick per unit
	toxinSelfResist   = 0.7   // emitter ignores 70% of own toxin tile damage
	toxinSprayDist    = 15.0  // toxin deposited this far ahead of the cell (directional spray)

	// Signals (pheromones)
	signalDiffusion = 0.08 // signals spread moderately fast (between oxygen and toxins)
	signalDecay     = 0.04 // signals fade fairly quickly — they're transient messages
	signalEmitRate  = 0.3  // amount of signal deposited per unit weight of actSignal
	signalBoostRate = 0.05 // nearby signal boosts photosynthesis/nutrient absorption (symbiotic reward)

	// Internal state (leaky integrators)
	stateDecayRate = 0.02 // state drifts toward zero each tick (~35 tick half-life)
	stateNudgeRate = 0.1  // how fast actSetState pushes state toward sensed value

	// Colony adhesion
	adhereStrength = 0.15 // attractive force toward nearest neighbor per unit weight
	adhereMaxForce = 0.08 // cap so cells orbit rather than collapse into a point

	// Energy sharing
	shareRate  = 0.05 // fraction of energy transferred per tick per unit weight
	shareRange = 6.0  // extra px beyond touching distance where sharing works

	// Predation
	eatDamageRate = 3.0  // damage per bite = attacker.Size * effectivePower * this
	eatEnergyGain = 0.5  // fraction of damage the attacker recovers as energy
	eatKillBonus  = 20.0 // bonus energy per size unit of prey on the killing blow
	eatRangeBonus = 4.0  // extra px beyond touching distance where bites land

	// Day/night cycle
	dayLengthTicks    = 6000  // ticks per full day/night cycle (~5 min at 1x speed)
	sunMaxShift       = 0.25  // grid columns shifted per row at sunrise/sunset (controls shadow angle)
	nightO2Baseline   = 0.20  // fraction of oxygenSurfaceAdd always added (dissolved O2, sun-independent)
	nightMetabMin     = 0.08  // minimum metabolic rate at full night (cells hibernate in darkness)

	// Depth
	nutrientSinkRate = 0.08  // fraction of nutrients that drift down each tick
	depthCostMax     = 0.3   // extra maintenance cost at the very bottom

	// Buoyancy
	// Small cells are light and float near the top; large cells are dense and sink.
	// Rest depth = 5% (minSize) to 60% (maxSize) of the bottle height.
	// Force is capped so cells can always overcome it by actively swimming.
	buoyancyStrength  = 0.002  // spring force toward rest depth per px of displacement
	buoyancyMaxForce  = 0.06   // max buoyancy force per tick (cells can always swim out)
	buoyancyTopFrac   = 0.05   // rest depth fraction for the smallest cell
	buoyancyBotFrac   = 0.60   // rest depth fraction for the largest cell
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
	senseLight       = 1
	senseEnergy      = 2
	senseNeighbor    = 3
	senseDist        = 4
	senseNutrient    = 5
	senseAge         = 6
	senseSize        = 7
	senseOxygen      = 8
	senseToxin       = 9
	senseNeighborDir = 10 // which side is nearest neighbor: 0=left, 0.5=ahead/behind, 1=right
	senseNutrientDir = 11 // which side has more nutrients: 0=left, 0.5=equal, 1=right
	senseLightDir    = 12 // which side is brighter: 0=left, 0.5=equal, 1=right
	senseSignal      = 13 // local pheromone concentration (0=none, 1=saturated)
	senseSignalDir   = 14 // which side has more signal: 0=left, 0.5=equal, 1=right
	// 15 is geneSTOP — skip it
	senseState0 = 16 // internal state register 0 (leaky integrator)
	senseState1 = 17 // internal state register 1 (leaky integrator)
	senseKin    = 18 // genome similarity to nearest neighbor (0=alien, 1=clone)
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
	actPhoto     = 1
	actForward   = 2 // move in facing direction
	actTurnLeft  = 3 // rotate heading counter-clockwise
	actTurnRight = 4 // rotate heading clockwise
	actEat       = 5
	actGrow      = 6
	actReproduce = 7
	actToxin     = 8
	actDefense   = 9
	actSignal    = 10 // emit pheromone into environment
	actSetState0 = 11 // nudge internal state register 0 toward sensed value
	actSetState1 = 12 // nudge internal state register 1 toward sensed value
	actAdhere    = 13 // attract toward nearest neighbor (colony adhesion)
	actShare     = 14 // transfer energy to nearest neighbor
	actMaxAction = 14
)

// --- Data structures ---

type ParsedGene struct {
	Sense     int
	Condition int
	Action    int
	Weight    float64
}

// AncestorRecord is a lightweight snapshot of a cell kept for the family-tree panel.
type AncestorRecord struct {
	ID         uint64
	Generation int
	ColorR, ColorG, ColorB uint8
	Size       float64
	Defense    float64
	GeneCount  int
}

const maxAncestors = 10 // max depth of ancestry chain kept per cell

type Cell struct {
	ID                     uint64
	X, Y                   float64
	VX, VY                 float64 // velocity
	PrevX                  float64
	PrevY                  float64
	Heading                float64 // facing direction in radians
	Energy                 float64
	MaxEnergy              float64
	Age                    int
	Size                   float64
	Defense                float64
	Genome                 []int
	Genes                  []ParsedGene
	ParentID               uint64
	Generation             int
	ReproCool              int     // ticks until can reproduce again
	Alive                  bool
	Toxicity               float64 // accumulated internal toxicity; death at >= 100
	DecomposeTicks         int     // counts down while corpse remains; removed when 0
	ColorR, ColorG, ColorB uint8   // cached display color
	Ancestors              []AncestorRecord // oldest first; capped at maxAncestors
	State                  [2]float64       // internal leaky integrator registers
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
						// Note: sense 15 (geneSTOP) can never reach here — the parser
						// consumes it as a structural marker before we get to validation.
						if sense >= senseLight && sense <= senseKin && sense != geneSTOP &&
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

// genomeSimilarity returns 0..1 measuring how alike two genomes are.
// Uses trigram Jaccard similarity — O(n+m) with no heap allocations.
// Packs each trigram (3 values in 0..18) into a single uint16 key for a fixed-size bitset.
func genomeSimilarity(a, b []int) float64 {
	la, lb := len(a), len(b)
	if la < 3 || lb < 3 {
		return 0
	}
	// Max trigram key: 18*19*19+18*19+18 = 6498+342+18 = 6858; 7000 bits ≈ 875 bytes
	const buckets = (7000 + 63) / 64 // 110 uint64s
	var setA [buckets]uint64
	var setB [buckets]uint64
	countA, countB := 0, 0

	for i := 0; i <= la-3; i++ {
		key := uint(a[i])*19*19 + uint(a[i+1])*19 + uint(a[i+2])
		idx, bit := key/64, key%64
		if setA[idx]&(1<<bit) == 0 {
			setA[idx] |= 1 << bit
			countA++
		}
	}
	for i := 0; i <= lb-3; i++ {
		key := uint(b[i])*19*19 + uint(b[i+1])*19 + uint(b[i+2])
		idx, bit := key/64, key%64
		if setB[idx]&(1<<bit) == 0 {
			setB[idx] |= 1 << bit
			countB++
		}
	}

	// Count intersection via bitwise AND + popcount
	inter := 0
	for i := range setA {
		v := setA[i] & setB[i]
		inter += popcount64(v)
	}
	union := countA + countB - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func popcount64(x uint64) int {
	// Hamming weight — compiler intrinsic on modern Go
	x = x - ((x >> 1) & 0x5555555555555555)
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}

func randomGenome(length int) []int {
	g := make([]int, length)
	for i := range g {
		g[i] = rand.Intn(19)
	}
	return g
}

func injectStarterGenes(g []int) []int {
	// Photosynthesis: sense light, always, photo, high weight
	photo := []int{0, senseLight, condAlways, actPhoto, 10, 15}
	// Reproduce: sense energy, high, reproduce, medium weight
	repro := []int{0, senseEnergy, condHigh, actReproduce, 8, 15}
	// Move toward light: always move forward (swim upward toward sun)
	move := []int{0, senseLight, condAlways, actForward, 5, 15}
	// Steer toward brighter side: turn right if right is brighter, left if left is brighter
	turnR := []int{0, senseLightDir, condHigh, actTurnRight, 7, 15}
	turnL := []int{0, senseLightDir, condLow, actTurnLeft, 7, 15}

	result := make([]int, 0, len(g)+len(photo)+len(repro)+len(move)+len(turnR)+len(turnL))
	result = append(result, photo...)
	result = append(result, repro...)
	result = append(result, move...)
	result = append(result, turnR...)
	result = append(result, turnL...)
	result = append(result, g...)
	return result
}

func injectPredatorGenes(g []int) []int {
	// Eat: always try to eat nearest neighbor
	eat := []int{0, senseDist, condLow, actEat, 11, 15}
	// Move forward: always chase (thrust toward prey)
	move := []int{0, senseDist, condLow, actForward, 10, 15}
	// Steer toward nearest neighbor using directional sense
	turnR := []int{0, senseNeighborDir, condHigh, actTurnRight, 9, 15}
	turnL := []int{0, senseNeighborDir, condLow, actTurnLeft, 9, 15}
	// Defense: build membrane when neighbors are close
	def := []int{0, senseDist, condLow, actDefense, 6, 15}
	// Reproduce: sense energy, high, reproduce
	repro := []int{0, senseEnergy, condHigh, actReproduce, 8, 15}

	result := make([]int, 0, len(g)+len(eat)+len(move)+len(turnR)+len(turnL)+len(def)+len(repro))
	result = append(result, eat...)
	result = append(result, move...)
	result = append(result, turnR...)
	result = append(result, turnL...)
	result = append(result, def...)
	result = append(result, repro...)
	result = append(result, g...)
	return result
}

func injectColonyGenes(g []int) []int {
	// Photosynthesis: core energy source
	photo := []int{0, senseLight, condAlways, actPhoto, 10, 15}
	// Adhere: stick to nearest neighbor when neighbors are close
	adhere := []int{0, senseDist, condLow, actAdhere, 9, 15}
	// Share: give energy to neighbor when own energy is high
	share := []int{0, senseEnergy, condHigh, actShare, 7, 15}
	// Signal: emit pheromone so colony-mates can find each other
	sig := []int{0, senseNeighbor, condLow, actSignal, 6, 15}
	// Reproduce
	repro := []int{0, senseEnergy, condHigh, actReproduce, 8, 15}

	result := make([]int, 0, len(g)+len(photo)+len(adhere)+len(share)+len(sig)+len(repro))
	result = append(result, photo...)
	result = append(result, adhere...)
	result = append(result, share...)
	result = append(result, sig...)
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
			out[i] = rand.Intn(19)
		}
	}

	// Insertion
	if rand.Float64() < 0.02 {
		pos := rand.Intn(len(out) + 1)
		val := rand.Intn(19)
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

// crossoverGenome splices two parent genomes at random cut points.
// The child gets a[:splitA] + b[splitB:], producing novel gene combinations.
func crossoverGenome(a, b []int) []int {
	if len(a) == 0 {
		return append([]int{}, b...)
	}
	if len(b) == 0 {
		return append([]int{}, a...)
	}
	splitA := rand.Intn(len(a) + 1)
	splitB := rand.Intn(len(b) + 1)
	child := make([]int, 0, splitA+len(b)-splitB)
	child = append(child, a[:splitA]...)
	child = append(child, b[splitB:]...)
	return child
}

// --- Spatial hash (fixed grid, index-based) ---

const (
	hashW = screenWidth/int(bucketSize) + 2
	hashH = screenHeight/int(bucketSize) + 2
)

type SpatialHash struct {
	buckets [hashW][hashH][]int // stores cell indices into Sim.cells
	dirty   [][2]int            // which buckets were written this tick
	scratch []int               // reused query result buffer
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

func (s *SpatialHash) insert(idx int, x, y float64) {
	bx := int(x/bucketSize) % hashW
	by := int(y / bucketSize)
	if bx < 0 {
		bx += hashW
	}
	if by < 0 || by >= hashH {
		return
	}
	if len(s.buckets[bx][by]) == 0 {
		s.dirty = append(s.dirty, [2]int{bx, by})
	}
	s.buckets[bx][by] = append(s.buckets[bx][by], idx)
}

// query appends nearby cell indices into s.scratch and returns it.
func (s *SpatialHash) query(x, y, radius float64, cells []Cell) []int {
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
			for _, idx := range s.buckets[wbx][by] {
				if cells[idx].Alive {
					s.scratch = append(s.scratch, idx)
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
			n.tmp[x][y] = (n.data[x][y] + (avg-n.data[x][y])*diffusionRate) * (1.0 - nutrientDecay)
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
	retain := 1.0 - decayRate
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < nutrientGridH; y++ {
			center := g.data[x][y]
			// Skip tiles where center and all neighbors are effectively zero
			if center < 1e-8 {
				n0 := x > 0 && g.data[x-1][y] > 1e-8
				n1 := x < nutrientGridW-1 && g.data[x+1][y] > 1e-8
				n2 := y > 0 && g.data[x][y-1] > 1e-8
				n3 := y < nutrientGridH-1 && g.data[x][y+1] > 1e-8
				if !n0 && !n1 && !n2 && !n3 {
					g.tmp[x][y] = 0
					continue
				}
			}
			sum := center
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
			val := (center + (avg-center)*diffRate) * retain
			if val < 0 {
				val = 0
			}
			g.tmp[x][y] = val
		}
	}
	g.data = g.tmp
}

// --- Simulation ---

type Sim struct {
	cells    []Cell
	hash     *SpatialHash
	nutrients NutrientGrid
	oxygen   EnvGrid
	toxins   EnvGrid
	signals  EnvGrid
	lightMap [nutrientGridW][nutrientGridH]float64 // recomputed each tick
	nextID   uint64
	tick     int
	sunAngle float64 // 0=sunrise (left), π/2=noon, π=sunset (right), π..2π=night
	paused   bool
	speed    int    // ticks per frame
	newCells []Cell // reused buffer for children each tick
}

func newSim() *Sim {
	s := &Sim{
		hash:   newSpatialHash(),
		nextID: 1,
		speed:  1,
	}
	s.seed()
	return s
}

func (s *Sim) seed() {
	s.cells = s.cells[:0]
	s.nextID = 1
	s.tick = 0
	s.sunAngle = math.Pi / 6 // start at early morning (~30°): enough light to photosynthesize, ~2500 ticks before first night
	s.nutrients = NutrientGrid{}
	s.oxygen = EnvGrid{}
	s.toxins = EnvGrid{}
	s.signals = EnvGrid{}

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
		} else if i < seededCells+10 {
			// Seed 5 colony cells: photosynthesize, adhere, share, signal
			genome = injectColonyGenes(genome)
		}
		c := Cell{
			ID:        s.nextID,
			X:         rand.Float64() * screenWidth,
			Y:         rand.Float64() * screenHeight * 0.6,
			Heading:   rand.Float64() * 2 * math.Pi,
			Energy:    100.0,
			MaxEnergy: 1000.0,
			Size:      3.0,
			Genome:    genome,
			Genes:     parseGenome(genome),
			Alive:     true,
		}
		c.PrevX = c.X
		c.PrevY = c.Y
		computeCellColor(&c)
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

func (s *Sim) senseValue(c *Cell, sense int, nearestIdx int, neighborCount int, nearestDist float64, cachedKin *float64) float64 {
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
		v := float64(neighborCount) / 10.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseDist:
		if nearestIdx < 0 {
			return 1.0
		}
		v := nearestDist / 200.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseNutrient:
		v := s.nutrients.getAt(c.X, c.Y) / 10.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseAge:
		v := float64(c.Age) / 10000.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseSize:
		return c.Size / maxSize
	case senseOxygen:
		v := s.oxygen.getAt(c.X, c.Y) / 5.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseToxin:
		v := s.toxins.getAt(c.X, c.Y) / 5.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseNeighborDir:
		// Which side is the nearest neighbor on?
		// Returns 0=fully left, 0.5=ahead or behind (neutral), 1=fully right
		if nearestIdx < 0 {
			return 0.5
		}
		n := &s.cells[nearestIdx]
		dx := n.X - c.X
		dy := n.Y - c.Y
		angleToNeighbor := math.Atan2(dy, dx)
		rel := angleToNeighbor - c.Heading
		for rel > math.Pi {
			rel -= 2 * math.Pi
		}
		for rel < -math.Pi {
			rel += 2 * math.Pi
		}
		// sin(rel): +1 = 90° right, 0 = ahead/behind, -1 = 90° left
		return (math.Sin(rel) + 1.0) / 2.0
	case senseNutrientDir:
		// Which side has more nutrients? Sample perpendicular to heading.
		sampleD := c.Size * 4
		lx := c.X + math.Cos(c.Heading-math.Pi/2)*sampleD
		ly := c.Y + math.Sin(c.Heading-math.Pi/2)*sampleD
		rx := c.X + math.Cos(c.Heading+math.Pi/2)*sampleD
		ry := c.Y + math.Sin(c.Heading+math.Pi/2)*sampleD
		left := s.nutrients.getAt(lx, ly)
		right := s.nutrients.getAt(rx, ry)
		total := left + right
		if total < 0.001 {
			return 0.5
		}
		return right / total
	case senseLightDir:
		// Which side is brighter? Sample perpendicular to heading.
		sampleD := c.Size * 4
		lx := c.X + math.Cos(c.Heading-math.Pi/2)*sampleD
		ly := c.Y + math.Sin(c.Heading-math.Pi/2)*sampleD
		rx := c.X + math.Cos(c.Heading+math.Pi/2)*sampleD
		ry := c.Y + math.Sin(c.Heading+math.Pi/2)*sampleD
		lgx, lgy := int(lx/nutrientCellW), int(ly/nutrientCellH)
		rgx, rgy := int(rx/nutrientCellW), int(ry/nutrientCellH)
		var left, right float64
		if lgx >= 0 && lgx < nutrientGridW && lgy >= 0 && lgy < nutrientGridH {
			left = s.lightMap[lgx][lgy]
		}
		if rgx >= 0 && rgx < nutrientGridW && rgy >= 0 && rgy < nutrientGridH {
			right = s.lightMap[rgx][rgy]
		}
		total := left + right
		if total < 0.001 {
			return 0.5
		}
		return right / total
	case senseSignal:
		v := s.signals.getAt(c.X, c.Y) / 5.0
		if v > 1.0 {
			v = 1.0
		}
		return v
	case senseSignalDir:
		// Which side has more pheromone? Sample perpendicular to heading.
		sampleD := c.Size * 4
		lx := c.X + math.Cos(c.Heading-math.Pi/2)*sampleD
		ly := c.Y + math.Sin(c.Heading-math.Pi/2)*sampleD
		rx := c.X + math.Cos(c.Heading+math.Pi/2)*sampleD
		ry := c.Y + math.Sin(c.Heading+math.Pi/2)*sampleD
		left := s.signals.getAt(lx, ly)
		right := s.signals.getAt(rx, ry)
		total := left + right
		if total < 0.001 {
			return 0.5
		}
		return right / total
	case senseState0:
		return c.State[0]
	case senseState1:
		return c.State[1]
	case senseKin:
		// cachedKin is a pointer to the caller's local; compute on first access
		if *cachedKin < 0 {
			if nearestIdx >= 0 {
				*cachedKin = genomeSimilarity(c.Genome, s.cells[nearestIdx].Genome)
			} else {
				*cachedKin = 0
			}
		}
		return *cachedKin
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
	// Rebuild spatial hash + crowding map in one pass
	s.hash.clear()
	var crowding [nutrientGridW][nutrientGridH]int
	for i := range s.cells {
		c := &s.cells[i]
		if !c.Alive {
			continue
		}
		s.hash.insert(i, c.X, c.Y)
		gx := int(c.X / nutrientCellW)
		gy := int(c.Y / nutrientCellH)
		if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
			crowding[gx][gy]++
		}
	}

	// Advance sun angle — full cycle every dayLengthTicks
	s.sunAngle += 2 * math.Pi / dayLengthTicks
	if s.sunAngle >= 2*math.Pi {
		s.sunAngle -= 2 * math.Pi
	}
	sunElevation := math.Sin(s.sunAngle)
	if sunElevation < 0 {
		sunElevation = 0
	}
	// metabolicFactor: cells slow all energy costs at night (hibernation-like)
	// ranges from nightMetabMin (full dark) to 1.0 (full sun)
	metabolicFactor := nightMetabMin + (1.0-nightMetabMin)*sunElevation
	// Horizontal shift per row: positive = sun on left (rays lean right), negative = sun on right
	sunXShift := math.Cos(s.sunAngle) * sunMaxShift

	// Build light map — angled rays cast from each surface column toward the sun's direction.
	// Clear first since multiple rays may overlap.
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < nutrientGridH; y++ {
			s.lightMap[x][y] = 0
		}
	}
	for x := 0; x < nutrientGridW; x++ {
		light := sunElevation
		for y := 0; y < nutrientGridH; y++ {
			dstX := x + int(math.Round(float64(y)*sunXShift))
			if dstX < 0 || dstX >= nutrientGridW {
				break
			}
			depthLight := 1.0 - float64(y)/float64(nutrientGridH)
			val := depthLight * light
			if val > s.lightMap[dstX][y] {
				s.lightMap[dstX][y] = val
			}
			if n := crowding[dstX][y]; n > 0 {
				// Attenuate light by 8% per cell: 0.92^n via precomputed pow
				light *= math.Pow(0.92, float64(n))
			}
		}
	}

	// Reuse new cells buffer
	s.newCells = s.newCells[:0]
	livingCount := 0

	for i := range s.cells {
		c := &s.cells[i]
		if !c.Alive {
			continue
		}
		livingCount++
		c.PrevX = c.X
		c.PrevY = c.Y

		// Find neighbors + separation in one pass (merged from two separate loops)
		searchRadius := c.Size * 3
		if searchRadius < 50.0 {
			searchRadius = 50.0
		}
		searchRadiusSq := searchRadius * searchRadius
		nearby := s.hash.query(c.X, c.Y, searchRadius, s.cells)

		nearestIdx := -1
		nearestDistSq := math.MaxFloat64
		neighborCount := 0

		for _, nIdx := range nearby {
			if nIdx == i {
				continue
			}
			n := &s.cells[nIdx]
			if !n.Alive {
				continue
			}

			dx := n.X - c.X
			dy := n.Y - c.Y
			dSq := dx*dx + dy*dy

			if dSq < searchRadiusSq {
				neighborCount++
				if dSq < nearestDistSq {
					nearestDistSq = dSq
					nearestIdx = nIdx
				}
			}

			// Separation — push overlapping cells apart (physical, not gene-driven)
			minDist := c.Size + n.Size
			if dSq < minDist*minDist && dSq > 0.000001 {
				d := math.Sqrt(dSq)
				overlap := minDist - d
				invD := 1.0 / d
				// Force proportional to overlap, split by mass (size)
				totalSize := c.Size + n.Size
				cShare := n.Size / totalSize // bigger neighbour pushes c more
				force := overlap * 0.25 * cShare
				c.VX -= dx * invD * force
				c.VY -= dy * invD * force
			}
		}

		// Compute nearest distance only once (single sqrt instead of per-neighbor)
		var nearestDist float64
		if nearestIdx >= 0 {
			nearestDist = math.Sqrt(nearestDistSq)
		}

		// Lazy kin similarity — only computed if a gene actually uses senseKin
		cachedKin := -1.0 // sentinel: not yet computed

		// Accumulate actions — fixed array, no allocation
		var actions [actMaxAction + 1]float64
		for _, g := range c.Genes {
			if g.Action >= 1 && g.Action <= actMaxAction {
				val := s.senseValue(c, g.Sense, nearestIdx, neighborCount, nearestDist, &cachedKin)
				if checkCondition(val, g.Condition) {
					// setState actions nudge the register toward the sensed value immediately;
					// multiple genes compound their nudges like real receptor integration.
					if g.Action == actSetState0 {
						c.State[0] += stateNudgeRate * g.Weight * (val - c.State[0])
					} else if g.Action == actSetState1 {
						c.State[1] += stateNudgeRate * g.Weight * (val - c.State[1])
					} else {
						actions[g.Action] += g.Weight
					}
				}
			}
		}

		// accel scales force by 1/size: big cells accelerate slowly
		accel := 0.4 / c.Size

		// Signal boost — local pheromone concentration rewards cells that cluster near signallers.
		// This gives signals an immediate evolutionary payoff: being near signal = better feeding.
		localSignal := s.signals.getAt(c.X, c.Y)
		signalBoost := 1.0
		if localSignal > 0 {
			boost := localSignal * signalBoostRate
			if boost > 0.3 {
				boost = 0.3 // cap at 30% bonus
			}
			signalBoost = 1.0 + boost
		}

		// Apply actions (array index, no allocation)
		if w := actions[actPhoto]; w > 0 {
			// Use light map — accounts for shadowing by cells above
			clx := int(c.X / nutrientCellW)
			cly := int(c.Y / nutrientCellH)
			light := 0.0
			if clx >= 0 && clx < nutrientGridW && cly >= 0 && cly < nutrientGridH {
				light = s.lightMap[clx][cly]
			}
			// Thick defensive membrane blocks light absorption
			photoEff := 1.0 - c.Defense*defensePhotoMul
			photoOutput := light * w * c.Size * photoEff * signalBoost
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

		if w := actions[actEat]; w > 0 && nearestIdx >= 0 {
			n := &s.cells[nearestIdx]
			if nearestDist < c.Size+n.Size+eatRangeBonus {
				// Defense scales down damage; high defense still hurts but not immune
				effectivePower := w * (1.0 - n.Defense*0.7)
				if effectivePower <= 0 {
					c.Energy -= 0.5 // teeth bounce off armoured prey
				} else {
					damage := c.Size * effectivePower * eatDamageRate
					c.Energy += damage * eatEnergyGain
					prevEnergy := n.Energy
					n.Energy -= damage
					// Kill bonus: predator that lands the finishing blow gets a windfall
					if prevEnergy > 0 && n.Energy <= 0 {
						c.Energy += n.Size * eatKillBonus
					}
				}
			}
		}

		if w := actions[actGrow]; w > 0 {
			growth := 0.01 * w
			cost := growth * growEnergyCost * c.Size // bigger cells pay more to grow
			if c.Energy > cost {
				c.Energy -= cost
				c.Size += growth
			}
			if c.Size > maxSize {
				c.Size = maxSize
			}
		}

		if w := actions[actReproduce]; w > 0 {
			if c.ReproCool <= 0 && c.Energy > reproThreshold*c.Size && livingCount+len(s.newCells) < maxCells {
				c.ReproCool = 100

				// Attempt sexual reproduction: find a nearby willing mate
				mateIdx := -1
				matingRange := (c.Size + 1) * 4
				if nearestIdx >= 0 && nearestDist < matingRange {
					mate := &s.cells[nearestIdx]
					if mate.Alive && mate.ReproCool <= 0 && mate.Energy > reproThreshold*mate.Size {
						mateIdx = nearestIdx
					}
				}

				var childEnergy float64
				var childGenome []int

				if mateIdx >= 0 {
					// Sexual: both parents each donate 25% energy; child gets the sum
					mate := &s.cells[mateIdx]
					contrib := c.Energy * 0.25
					mateContrib := mate.Energy * 0.25
					c.Energy -= contrib
					mate.Energy -= mateContrib
					mate.ReproCool = 100
					childEnergy = contrib + mateContrib
					childGenome = mutateGenome(crossoverGenome(c.Genome, mate.Genome))
				} else {
					// Asexual fallback: clone + mutate
					childEnergy = c.Energy * 0.5
					c.Energy -= childEnergy
					childGenome = mutateGenome(c.Genome)
				}

				childSize := c.Size * 0.8
				if childSize < minSize {
					childSize = minSize
				}

				// Build ancestry chain for child: parent's chain + parent record
				rec := AncestorRecord{
					ID: c.ID, Generation: c.Generation,
					ColorR: c.ColorR, ColorG: c.ColorG, ColorB: c.ColorB,
					Size: c.Size, Defense: c.Defense, GeneCount: len(c.Genes),
				}
				anc := make([]AncestorRecord, 0, len(c.Ancestors)+1)
				anc = append(anc, c.Ancestors...)
				anc = append(anc, rec)
				if len(anc) > maxAncestors {
					anc = anc[len(anc)-maxAncestors:]
				}

				child := Cell{
					ID:         s.nextID,
					X:          wrapX(c.X + (rand.Float64()-0.5)*c.Size),
					Y:          clampY(c.Y + (rand.Float64()-0.5)*c.Size),
					Heading:    rand.Float64() * 2 * math.Pi,
					Energy:     childEnergy,
					MaxEnergy:  c.MaxEnergy,
					Size:       childSize,
					Genome:     childGenome,
					Genes:      parseGenome(childGenome),
					ParentID:   c.ID,
					Generation: c.Generation + 1,
					Alive:      true,
					Ancestors:  anc,
				}
				child.PrevX = child.X
				child.PrevY = child.Y
				computeCellColor(&child)
				s.nextID++
				s.newCells = append(s.newCells, child)
			}
		}

		if w := actions[actToxin]; w > 0 {
			// Spray toxins ahead of the cell (directional, not at own feet)
			sprayX := c.X + math.Cos(c.Heading)*toxinSprayDist
			sprayY := c.Y + math.Sin(c.Heading)*toxinSprayDist
			s.toxins.addAt(sprayX, sprayY, w*c.Size*0.5)
			c.Energy -= 0.5 * w
		}

		if w := actions[actDefense]; w > 0 {
			c.Defense += 0.01 * w
			if c.Defense > maxDefense {
				c.Defense = maxDefense
			}
		}

		if w := actions[actSignal]; w > 0 {
			// Emit pheromone into the environment — cheap but not free
			s.signals.addAt(c.X, c.Y, w*c.Size*signalEmitRate)
			c.Energy -= 0.1 * w
		}

		if w := actions[actAdhere]; w > 0 && nearestIdx >= 0 {
			// Attract toward nearest neighbor — the glue that holds colonies together.
			// Force scales with weight but is capped so cells orbit rather than merge.
			n := &s.cells[nearestIdx]
			dx := n.X - c.X
			dy := n.Y - c.Y
			d := math.Sqrt(dx*dx + dy*dy)
			// Only attract when outside touching distance (inside, separation still pushes)
			restDist := c.Size + n.Size + 2.0
			if d > restDist && d > 0.001 {
				pull := w * adhereStrength / c.Size
				if pull > adhereMaxForce {
					pull = adhereMaxForce
				}
				invD := 1.0 / d
				c.VX += dx * invD * pull
				c.VY += dy * invD * pull
			}
		}

		if w := actions[actShare]; w > 0 && nearestIdx >= 0 {
			// Transfer energy to nearest neighbor — enables cooperative colonies.
			// Only shares if neighbor is close enough and donor has surplus.
			n := &s.cells[nearestIdx]
			if nearestDist < c.Size+n.Size+shareRange {
				give := c.Energy * shareRate * w
				if give > c.Energy*0.25 {
					give = c.Energy * 0.25 // never give more than 25% per tick
				}
				c.Energy -= give
				n.Energy += give * 0.9 // 10% transfer loss (thermodynamic cost)
				if n.Energy > n.MaxEnergy {
					n.Energy = n.MaxEnergy
				}
			}
		}

		// Passive drift — tiny random nudge into velocity
		driftForce := 0.05 / c.Size
		c.VX += (rand.Float64() - 0.5) * driftForce * 2
		c.VY += (rand.Float64() - 0.5) * driftForce * 2

		// Buoyancy — cells drift toward their natural rest depth based on size.
		// Small cells float near the surface; large cells sink toward the bottom.
		// Force is capped so a cell can always overcome it by actively swimming.
		bottleHeight := bottleBottom - bottleTop
		sizeNorm := (c.Size - minSize) / (maxSize - minSize) // 0 (small) → 1 (large)
		restY := bottleTop + (buoyancyTopFrac+sizeNorm*(buoyancyBotFrac-buoyancyTopFrac))*bottleHeight
		bf := (restY - c.Y) * buoyancyStrength
		if bf > buoyancyMaxForce {
			bf = buoyancyMaxForce
		} else if bf < -buoyancyMaxForce {
			bf = -buoyancyMaxForce
		}
		c.VY += bf

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

		// Drag — larger cells have more drag; armored cells even more
		baseDrag := 0.12 + c.Size*0.008
		armorDrag := c.Defense * defenseDragMul * 0.1 // up to +0.04 extra drag at max defense
		drag := 1.0 - (baseDrag + armorDrag)
		c.VX *= drag
		c.VY *= drag

		// Cap speed — use squared comparison to avoid sqrt in common case
		spdSq := c.VX*c.VX + c.VY*c.VY
		maxSpd := maxSpeed / c.Size
		if spdSq > maxSpd*maxSpd {
			scale := maxSpd / math.Sqrt(spdSq)
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

		// Nutrient absorption — boosted by nearby pheromone (signal symbiosis)
		c.Energy += s.nutrients.absorbAt(c.X, c.Y, 0.1) * signalBoost

		// Oxygen consumption — scales with crowding
		localO2 := s.oxygen.getAt(c.X, c.Y)
		gx := int(c.X / nutrientCellW)
		gy := int(c.Y / nutrientCellH)
		crowd := 1.0
		if gx >= 0 && gx < nutrientGridW && gy >= 0 && gy < nutrientGridH {
			cv := float64(crowding[gx][gy])
			if cv > 1.0 {
				crowd = cv
			}
		}
		// More cells on the same tile = each cell needs more oxygen (competition)
		crowdFactor := 1.0 + (crowd-1)*0.3
		breathNeeded := oxygenBreathRate * c.Size * crowdFactor * metabolicFactor
		if localO2 >= breathNeeded {
			s.oxygen.addAt(c.X, c.Y, -breathNeeded)
		} else {
			// Not enough oxygen — consume what's there, pay energy penalty
			s.oxygen.addAt(c.X, c.Y, -localO2)
			deficit := 1.0 - localO2/breathNeeded // 0..1
			c.Energy -= lowOxygenPenalty * deficit * c.Size
		}

		// Toxin damage — local toxins hurt cells and accumulate internal toxicity
		localToxin := s.toxins.getAt(c.X, c.Y)
		if localToxin > 0 {
			// Cells actively producing toxins have evolved resistance to their own poison
			selfResist := 0.0
			if actions[actToxin] > 0 {
				selfResist = toxinSelfResist
			}
			resist := 1.0 - c.Defense*0.5 - selfResist
			if resist < 0.05 {
				resist = 0.05 // never fully immune
			}
			dmg := localToxin * toxinDamage * resist
			c.Energy -= dmg
			c.Toxicity += localToxin * toxicityBuildRate * resist
		}

		// Defense maintenance — armor is metabolically expensive
		if c.Defense > 0 {
			c.Energy -= c.Defense * defenseCostRate * c.Size * metabolicFactor
		}

		// Maintenance cost scaled by metabolic rate (cheaper at night — cells hibernate)
		depth := c.Y / screenHeight // 0 at top, 1 at bottom
		baseCost := 0.3 + (c.Size*0.1) + (float64(len(c.Genes))*0.05) + (depth*depthCostMax)
		c.Energy -= baseCost * metabolicFactor

		// Cap energy
		if c.Energy > c.MaxEnergy {
			c.Energy = c.MaxEnergy
		}

		// State decay — leaky integrators drift toward zero
		c.State[0] *= 1.0 - stateDecayRate
		c.State[1] *= 1.0 - stateDecayRate

		// Starvation shrinking — cells consume their own mass when starving
		if c.Energy < c.MaxEnergy*0.1 && c.Size > minSize {
			c.Size -= starveShrinkRate * c.Size
			if c.Size < minSize {
				c.Size = minSize
			}
			c.Energy += 0.5 // small energy reclaimed from catabolized mass
		}

		// Aging & cooldowns
		c.Age++
		if c.ReproCool > 0 {
			c.ReproCool--
		}

		// Death
		if c.Energy <= 0 || c.Toxicity >= 100.0 {
			if c.Alive {
				c.Alive = false
				// Larger cells take longer to decompose: 60 ticks per size unit
				c.DecomposeTicks = int(corpseDecayPerSize * c.Size)
			}
		}
	}

	// Decompose corpses: drip nutrients and slowly sink as buoyancy is lost.
	// Fresh corpses barely move; fully decomposed corpses sink at max rate.
	// Total nutrients released = c.Size*2 regardless of size (rate = 2/corpseDecayPerSize per tick).
	nutrientsPerCorpseTick := 2.0 / corpseDecayPerSize
	for i := range s.cells {
		c := &s.cells[i]
		if !c.Alive && c.DecomposeTicks > 0 {
			s.nutrients.addAt(c.X, c.Y, c.Size*nutrientsPerCorpseTick)
			// Buoyancy lost fraction: 0 when fresh, 1 when nearly gone
			initialTicks := corpseDecayPerSize * c.Size
			decomposeFrac := 1.0 - float64(c.DecomposeTicks)/initialTicks
			sinkSpeed := decomposeFrac * 0.4 // max 0.4 px/tick when fully decomposed
			c.Y += sinkSpeed
			if c.Y > bottleBottom {
				c.Y = bottleBottom
			}
			c.DecomposeTicks--
		}
	}

	// Add new cells
	s.cells = append(s.cells, s.newCells...)

	// Compact fully-decomposed cells in-place every tick (no allocation)
	n := 0
	for i := range s.cells {
		if s.cells[i].Alive || s.cells[i].DecomposeTicks > 0 {
			if n != i {
				s.cells[n] = s.cells[i]
			}
			n++
		}
	}
	// Clear removed slots to release Genome/Genes slice references for GC
	for i := n; i < len(s.cells); i++ {
		s.cells[i] = Cell{}
	}
	s.cells = s.cells[:n]

	// Oxygen surface production (must precede diffusion)
	sunElev := math.Sin(s.sunAngle)
	if sunElev < 0 {
		sunElev = 0
	}
	o2scale := nightO2Baseline + (1.0-nightO2Baseline)*sunElev
	for x := 0; x < nutrientGridW; x++ {
		for y := 0; y < 10; y++ {
			v := s.oxygen.data[x][y] + oxygenSurfaceAdd*o2scale*(1.0-float64(y)/10.0)
			if v > 10.0 {
				v = 10.0
			}
			s.oxygen.data[x][y] = v
		}
		for y := 10; y < nutrientGridH; y++ {
			if s.oxygen.data[x][y] > 10.0 {
				s.oxygen.data[x][y] = 10.0
			}
		}
	}

	// Diffuse all 4 grids in parallel — they are independent
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { s.nutrients.diffuse(); wg.Done() }()
	go func() { s.oxygen.diffuse(oxygenDiffusion, oxygenDecay); wg.Done() }()
	go func() { s.toxins.diffuse(toxinDiffusion, toxinDecay); wg.Done() }()
	go func() { s.signals.diffuse(signalDiffusion, signalDecay); wg.Done() }()
	wg.Wait()

	s.tick++
}

// --- Cell color (computed once at creation, not per frame) ---

func computeCellColor(c *Cell) {
	var photoW, eatW, toxinW, signalW, adhereW, shareW float64
	var photoSenseSum, photoCount int

	for _, gene := range c.Genes {
		switch gene.Action {
		case actPhoto:
			photoW += gene.Weight
			photoSenseSum += gene.Sense
			photoCount++
		case actEat:
			eatW += gene.Weight
		case actToxin:
			toxinW += gene.Weight
		case actSignal:
			signalW += gene.Weight
		case actAdhere:
			adhereW += gene.Weight
		case actShare:
			shareW += gene.Weight
		}
	}

	var fr, fg, fb float64

	if photoW > 0 {
		avgSense := float64(photoSenseSum) / max(1.0, float64(photoCount))
		switch {
		case avgSense < 2: // light-sensing -> pure green
			fr, fg, fb = 0.2, 0.9, 0.2
		case avgSense < 3: // energy-sensing -> yellow-green
			fr, fg, fb = 0.6, 0.9, 0.1
		default: // neighbor-sensing -> teal
			fr, fg, fb = 0.1, 0.8, 0.7
		}
		sat := min(photoW, 1.0)
		fr = 0.5 + (fr-0.5)*sat
		fg = 0.5 + (fg-0.5)*sat
		fb = 0.5 + (fb-0.5)*sat
	} else if eatW > 0 {
		sat := min(eatW, 1.0)
		fr = 0.5 + 0.5*sat
		fg = 0.5 - 0.2*sat
		fb = 0.5 - 0.3*sat
	} else {
		fr, fg, fb = 0.5, 0.5, 0.5
	}

	// Toxin purple tint
	if toxinW > 0 {
		t := min(toxinW*0.5, 0.4)
		fr = fr*(1-t) + 0.7*t
		fg = fg*(1-t) + 0.2*t
		fb = fb*(1-t) + 0.9*t
	}

	// Signal yellow/amber tint
	if signalW > 0 {
		t := min(signalW*0.5, 0.4)
		fr = fr*(1-t) + 1.0*t
		fg = fg*(1-t) + 0.85*t
		fb = fb*(1-t) + 0.1*t
	}

	// Colony tint — cyan/blue when cells have adhesion or sharing genes
	colonyW := adhereW + shareW
	if colonyW > 0 {
		t := min(colonyW*0.3, 0.35)
		fr = fr*(1-t) + 0.2*t
		fg = fg*(1-t) + 0.8*t
		fb = fb*(1-t) + 1.0*t
	}

	c.ColorR = uint8(fr * 255)
	c.ColorG = uint8(fg * 255)
	c.ColorB = uint8(fb * 255)
}

// --- Save / Load ---

type SaveCell struct {
	ID         uint64  `json:"id"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	VX         float64 `json:"vx"`
	VY         float64 `json:"vy"`
	Heading    float64 `json:"heading"`
	Energy     float64 `json:"energy"`
	MaxEnergy  float64 `json:"max_energy"`
	Age        int     `json:"age"`
	Size       float64 `json:"size"`
	Defense    float64 `json:"defense"`
	Genome     []int      `json:"genome"`
	ParentID   uint64    `json:"parent_id"`
	Generation int       `json:"generation"`
	State      [2]float64 `json:"state,omitempty"`
}

type SaveData struct {
	Tick      int                                   `json:"tick"`
	NextID    uint64                                `json:"next_id"`
	SunAngle  float64                               `json:"sun_angle"`
	Cells     []SaveCell                            `json:"cells"`
	Nutrients [nutrientGridW][nutrientGridH]float64 `json:"nutrients"`
	Oxygen    [nutrientGridW][nutrientGridH]float64 `json:"oxygen"`
	Toxins    [nutrientGridW][nutrientGridH]float64 `json:"toxins"`
	Signals   [nutrientGridW][nutrientGridH]float64 `json:"signals,omitempty"`
}

func (s *Sim) export(path string) error {
	sd := SaveData{
		Tick:      s.tick,
		NextID:    s.nextID,
		SunAngle:  s.sunAngle,
		Nutrients: s.nutrients.data,
		Oxygen:    s.oxygen.data,
		Toxins:    s.toxins.data,
		Signals:   s.signals.data,
	}
	for i := range s.cells {
		c := &s.cells[i]
		if !c.Alive {
			continue
		}
		sd.Cells = append(sd.Cells, SaveCell{
			ID: c.ID, X: c.X, Y: c.Y, VX: c.VX, VY: c.VY,
			Heading: c.Heading, Energy: c.Energy, MaxEnergy: c.MaxEnergy,
			Age: c.Age, Size: c.Size, Defense: c.Defense,
			Genome: c.Genome, ParentID: c.ParentID, Generation: c.Generation,
			State: c.State,
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
	s.sunAngle = sd.SunAngle
	s.nutrients.data = sd.Nutrients
	s.oxygen.data = sd.Oxygen
	s.toxins.data = sd.Toxins
	s.signals.data = sd.Signals
	s.cells = make([]Cell, 0, len(sd.Cells))
	for _, sc := range sd.Cells {
		c := Cell{
			ID: sc.ID, X: sc.X, Y: sc.Y, VX: sc.VX, VY: sc.VY,
			Heading: sc.Heading, Energy: sc.Energy, MaxEnergy: sc.MaxEnergy,
			Age: sc.Age, Size: sc.Size, Defense: sc.Defense,
			Genome: sc.Genome, Genes: parseGenome(sc.Genome),
			ParentID: sc.ParentID, Generation: sc.Generation, Alive: true,
			State: sc.State,
		}
		c.PrevX = c.X
		c.PrevY = c.Y
		computeCellColor(&c)
		s.cells = append(s.cells, c)
	}
	return nil
}

// --- Game struct for Ebitengine ---

// simTickRates maps speed index -> sim ticks per second
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
	// label centered (ebitenutil uses 6x13 px chars)
	charW := 6
	tx := b.x + (b.w-len(b.label)*charW)/2
	ty := b.y + (b.h-13)/2
	ebitenutil.DebugPrintAt(screen, b.label, tx, ty)
}

// --- Population history ring buffer ---

const historyLen = 600 // data points kept (~30 sec at 20 tps sample rate)
const historySampleInterval = 10 // record a sample every N sim ticks

type HistorySample struct {
	Population   int
	MaxGen       int
	Phototrophs  int // cells with net positive photosynthesis weight
	Predators    int // cells with net positive eat weight
	Colonial     int // cells with adhere or share genes
	AvgO2        float64
	AvgToxin     float64
}

type HistoryRing struct {
	buf   [historyLen]HistorySample
	head  int  // next write position
	count int  // how many samples stored (max historyLen)
}

func (h *HistoryRing) push(s HistorySample) {
	h.buf[h.head] = s
	h.head = (h.head + 1) % historyLen
	if h.count < historyLen {
		h.count++
	}
}

// get returns the i-th sample (0 = oldest).
func (h *HistoryRing) get(i int) HistorySample {
	start := h.head - h.count
	if start < 0 {
		start += historyLen
	}
	return h.buf[(start+i)%historyLen]
}

type Game struct {
	sim            *Sim
	clickedCellID  uint64 // 0 means no selection
	camX, camY     float64
	zoom           float64
	dragging       bool
	dragLastX      int
	dragLastY      int
	hudCells       int
	hudMaxGen      int
	hudO2          float64 // cached average oxygen (updated every 10 ticks)
	hudTox         float64 // cached average toxin  (updated every 10 ticks)
	speedIdx       int
	tickAccum      float64
	speedButtons   []button
	actionButtons  []button // export, import
	statusMsg      string   // brief feedback message
	statusTick     int      // tick when status was set
	showFamilyTree bool
	showGraph      bool
	familyTreeBtn  button // bounds of the "Family Tree" button inside the cell popup
	history        HistoryRing
	lastHistTick   int // last tick when history was sampled
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

// findClickedCell looks up the selected cell by ID.
func (g *Game) findClickedCell() *Cell {
	if g.clickedCellID == 0 {
		return nil
	}
	for i := range g.sim.cells {
		if g.sim.cells[i].ID == g.clickedCellID && g.sim.cells[i].Alive {
			return &g.sim.cells[i]
		}
	}
	g.clickedCellID = 0
	return nil
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
		g.clickedCellID = 0
		g.speedIdx = 0
		g.tickAccum = 0
		g.history = HistoryRing{}
		g.lastHistTick = 0
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

	// Close family tree if selected cell died
	if g.showFamilyTree && g.findClickedCell() == nil {
		g.showFamilyTree = false
	}

	// Left click: buttons take priority, then cell inspect
	if mouseJustPressed(ebiten.MouseButtonLeft) {
		mx, my := ebiten.CursorPosition()
		hitButton := false

		// Family tree toggle button (inside popup)
		if g.clickedCellID != 0 && g.familyTreeBtn.contains(mx, my) {
			g.showFamilyTree = !g.showFamilyTree
			hitButton = true
		}

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
								g.clickedCellID = 0
							}
						} else {
							g.statusMsg = "No save file found"
						}
						g.statusTick = g.sim.tick
					case "Graph":
						g.showGraph = !g.showGraph
					}
					break
				}
			}
		}
		if !hitButton {
			wx, wy := g.screenToWorld(mx, my)
			var bestID uint64
			bestDist := math.MaxFloat64
			for i := range g.sim.cells {
				c := &g.sim.cells[i]
				if !c.Alive {
					continue
				}
				dx := wx - c.X
				dy := wy - c.Y
				d := math.Sqrt(dx*dx + dy*dy)
				if d < (c.Size+5)/g.zoom && d < bestDist {
					bestDist = d
					bestID = c.ID
				}
			}
			g.clickedCellID = bestID
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
			phototrophs, predators, colonial := 0, 0, 0
			for i := range g.sim.cells {
				c := &g.sim.cells[i]
				if !c.Alive {
					continue
				}
				living++
				if c.Generation > maxGen {
					maxGen = c.Generation
				}
				// Classify by dominant gene weights
				var photoW, eatW, adhW, shareW float64
				for _, gene := range c.Genes {
					switch gene.Action {
					case actPhoto:
						photoW += gene.Weight
					case actEat:
						eatW += gene.Weight
					case actAdhere:
						adhW += gene.Weight
					case actShare:
						shareW += gene.Weight
					}
				}
				if photoW > eatW {
					phototrophs++
				}
				if eatW > 0.3 {
					predators++
				}
				if adhW > 0 || shareW > 0 {
					colonial++
				}
			}
			g.hudCells = living
			g.hudMaxGen = maxGen

			// Compute grid averages only every 10 sim ticks (saves iterating 9600 tiles)
			if g.sim.tick%10 == 0 {
				var totalO2, totalTox float64
				for x := 0; x < nutrientGridW; x++ {
					for y := 0; y < nutrientGridH; y++ {
						totalO2 += g.sim.oxygen.data[x][y]
						totalTox += g.sim.toxins.data[x][y]
					}
				}
				gridCells := float64(nutrientGridW * nutrientGridH)
				g.hudO2 = totalO2 / gridCells
				g.hudTox = totalTox / gridCells
			}

			// Record history sample
			if g.sim.tick-g.lastHistTick >= historySampleInterval {
				g.history.push(HistorySample{
					Population:  living,
					MaxGen:      maxGen,
					Phototrophs: phototrophs,
					Predators:   predators,
					Colonial:    colonial,
					AvgO2:       g.hudO2,
					AvgToxin:    g.hudTox,
				})
				g.lastHistTick = g.sim.tick
			}
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

			light := g.sim.lightMap[gx][gy]
			o2 := g.sim.oxygen.data[gx][gy]
			tox := g.sim.toxins.data[gx][gy]

			// Skip tiles with nothing visible to draw
			if light > 0.99 && o2 < 0.1 && tox < 0.1 {
				continue
			}

			// Light: shadow overlay where sunlight is blocked
			shadow := 1.0 - light
			if shadow > 0.01 {
				a := uint8(shadow * 60)
				vector.FillRect(screen, sx, sy, tileSW, tileSH,
					color.RGBA{0x00, 0x00, 0x10, a}, false)
			}

			// Oxygen: subtle blue tint
			if o2 > 0.1 {
				ov := o2 / 5.0
				if ov > 1.0 {
					ov = 1.0
				}
				a := uint8(ov * 14)
				vector.FillRect(screen, sx, sy, tileSW, tileSH,
					color.RGBA{0x30, 0x70, 0xd0, a}, false)
			}

			// Toxin: purple-red tint
			if tox > 0.1 {
				tv := tox / 5.0
				if tv > 1.0 {
					tv = 1.0
				}
				a := uint8(tv * 25)
				vector.FillRect(screen, sx, sy, tileSW, tileSH,
					color.RGBA{0xa0, 0x20, 0x80, a}, false)
			}
		}
	}

	// Draw cells
	for i := range g.sim.cells {
		c := &g.sim.cells[i]

		sx, sy := g.worldToScreen(c.X, c.Y)
		z := float32(g.zoom)

		// Draw corpses as faded grey-brown blobs
		if !c.Alive {
			if c.DecomposeTicks <= 0 {
				continue
			}
			// Cull
			if sx < -50 || sx > screenWidth+50 || sy < -50 || sy > screenHeight+50 {
				continue
			}
			initialTicks := int(corpseDecayPerSize * c.Size)
			fade := uint8(180 * c.DecomposeTicks / initialTicks)
			corpseColor := color.RGBA{100, 80, 50, fade}
			vector.FillCircle(screen, sx, sy, float32(c.Size)*z, corpseColor, false)
			continue
		}

		r, gb, b := c.ColorR, c.ColorG, c.ColorB
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
			trailR := float32(c.Size * 0.5)
			if trailR < 1 {
				trailR = 1
			}
			vector.FillCircle(screen, px, py, trailR*z, trailColor, false)
		}

		// Border
		borderThickness := 1.0 + c.Defense*3.0
		borderR := r + 40
		if borderR < r {
			borderR = 255
		}
		borderG := gb + 40
		if borderG < gb {
			borderG = 255
		}
		borderB := b + 40
		if borderB < b {
			borderB = 255
		}
		borderColor := color.RGBA{borderR, borderG, borderB, 0xff}

		// Draw border circle then fill
		vector.FillCircle(screen, sx, sy, float32(c.Size+borderThickness)*z, borderColor, false)
		vector.FillCircle(screen, sx, sy, float32(c.Size)*z, fillColor, false)

		// Internal details (nucleus dots)
		geneCount := len(c.Genes)
		if geneCount >= 5 && c.Size > 3 {
			dotColor := color.RGBA{borderR, borderG, borderB, 200}
			dotR := float32(c.Size * 0.2)
			if dotR < 1 {
				dotR = 1
			}
			vector.FillCircle(screen, sx, sy, dotR*z, dotColor, false)
		}
		if geneCount >= 8 && c.Size > 4 {
			dotColor := color.RGBA{borderR, borderG, borderB, 160}
			ox, oy := g.worldToScreen(c.X+c.Size*0.3, c.Y-c.Size*0.3)
			dotR := float32(c.Size * 0.15)
			if dotR < 1 {
				dotR = 1
			}
			vector.FillCircle(screen, ox, oy, dotR*z, dotColor, false)
		}
	}

	// Sun / night overlay
	{
		sunElev := math.Sin(g.sim.sunAngle)
		if sunElev < 0 {
			sunElev = 0
		}
		sunXFrac := (1 - math.Cos(g.sim.sunAngle)) / 2 // 0=left, 0.5=center, 1=right

		// Night darkness overlay over the whole bottle interior
		nightAlpha := uint8((1.0 - sunElev) * 160)
		if nightAlpha > 0 {
			tlX, tlY := g.worldToScreen(bottleLeft, bottleTop)
			brX, brY := g.worldToScreen(bottleRight, bottleBottom)
			vector.FillRect(screen, tlX, tlY, brX-tlX, brY-tlY,
				color.RGBA{0x00, 0x00, 0x08, nightAlpha}, false)
		}

		// Draw sun above the bottle when it's above the horizon
		if sunElev > 0 {
			sunWorldX := bottleLeft + sunXFrac*(bottleRight-bottleLeft)
			sunWorldY := bottleTop - 18 - sunElev*50
			sx, sy := g.worldToScreen(sunWorldX, sunWorldY)
			radius := float32(8+sunElev*6) * float32(g.zoom)
			// Glow
			glowA := uint8(sunElev * 60)
			vector.FillCircle(screen, sx, sy, radius*2.2, color.RGBA{0xff, 0xd7, 0x00, glowA}, false)
			// Core
			vector.FillCircle(screen, sx, sy, radius, color.RGBA{0xff, 0xee, 0x88, 0xff}, false)
		}

		// Draw moon on the opposite side of the sky during night
		moonAngle := g.sim.sunAngle + math.Pi
		moonElev := math.Sin(moonAngle) // positive when sun is below horizon
		if moonElev > 0 {
			moonXFrac := (1 - math.Cos(moonAngle)) / 2
			moonWorldX := bottleLeft + moonXFrac*(bottleRight-bottleLeft)
			moonWorldY := bottleTop - 18 - moonElev*50
			mx, my := g.worldToScreen(moonWorldX, moonWorldY)
			radius := float32(6+moonElev*3) * float32(g.zoom)
			// Soft blue-white glow
			glowA := uint8(moonElev * 35)
			vector.FillCircle(screen, mx, my, radius*2.0, color.RGBA{0xb0, 0xc8, 0xff, glowA}, false)
			// Core — silvery white
			vector.FillCircle(screen, mx, my, radius, color.RGBA{0xe8, 0xee, 0xff, 0xff}, false)
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

	// HUD text (uses cached averages)
	pausedStr := ""
	if g.sim.paused {
		pausedStr = "  [PAUSED]"
	}
	dayFrac := g.sim.sunAngle / (2 * math.Pi) // 0..1 through the full cycle
	var timeStr string
	switch {
	case dayFrac < 0.25:
		timeStr = "Dawn"
	case dayFrac < 0.5:
		timeStr = "Dusk"
	case dayFrac < 0.75:
		timeStr = "Night"
	default:
		timeStr = "Pre-dawn"
	}
	sunElev := math.Sin(g.sim.sunAngle)
	if sunElev > 0 {
		if dayFrac < 0.25 {
			timeStr = "Morning"
		} else {
			timeStr = "Afternoon"
		}
	}
	hud := fmt.Sprintf("Cells: %d   Gen: %d   Tick: %d   O2: %.2f   Tox: %.2f   %s   Zoom: %.1fx%s",
		g.hudCells, g.hudMaxGen, g.sim.tick, g.hudO2, g.hudTox, timeStr, g.zoom, pausedStr)
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
	for i, label := range []string{"Export", "Import", "Graph"} {
		abtnW := 52
		btn := button{
			x:     actionStartX + i*(abtnW+btnPad),
			y:     startY,
			w:     abtnW,
			h:     btnH,
			label: label,
		}
		g.actionButtons = append(g.actionButtons, btn)
		active := label == "Graph" && g.showGraph
		drawButton(screen, btn, active)
	}

	// Status message (fades after a few seconds)
	if g.statusMsg != "" && g.sim.tick-g.statusTick < 200 {
		ebitenutil.DebugPrintAt(screen, g.statusMsg, 8, startY+btnH+6)
	}

	// Cell inspector popup + family tree
	if cc := g.findClickedCell(); cc != nil {
		g.drawCellPopup(screen, cc)
		if g.showFamilyTree {
			g.drawFamilyTree(screen, cc)
		}
	}

	// Population graph
	if g.showGraph {
		g.drawGraph(screen)
	}
}

func (g *Game) drawCellPopup(screen *ebiten.Image, c *Cell) {
	const (
		popW    = 300
		popX    = screenWidth - popW - 10
		popY    = 10
		lineH   = 14
		padding = 8
	)

	senseNames := map[int]string{1: "light", 2: "energy", 3: "neighbors", 4: "dist", 5: "nutrient", 6: "age", 7: "size", 8: "oxygen", 9: "toxin", 10: "neighborDir", 11: "nutrientDir", 12: "lightDir", 13: "signal", 14: "signalDir", 16: "state0", 17: "state1", 18: "kin"}
	condNames  := map[int]string{1: "HIGH", 2: "LOW", 3: "MED", 4: "ALWAYS"}
	actNames   := map[int]string{1: "photo", 2: "forward", 3: "turnLeft", 4: "turnRight", 5: "eat", 6: "grow", 7: "reproduce", 8: "toxin", 9: "defense", 10: "signal", 11: "setState0", 12: "setState1", 13: "adhere", 14: "share"}

	lines := []string{
		fmt.Sprintf("Cell #%d  (gen %d)", c.ID, c.Generation),
		fmt.Sprintf("Age:     %d", c.Age),
		fmt.Sprintf("Energy:  %.1f / %.1f", c.Energy, c.MaxEnergy),
		fmt.Sprintf("Toxicity: %.1f / 100", c.Toxicity),
		fmt.Sprintf("Size:    %.1f", c.Size),
		fmt.Sprintf("Defense: %.2f", c.Defense),
		fmt.Sprintf("State:   [%.2f, %.2f]", c.State[0], c.State[1]),
		fmt.Sprintf("Genes:   %d  (genome len %d)", len(c.Genes), len(c.Genome)),
		"",
	}
	for i, gene := range c.Genes {
		sn := senseNames[gene.Sense]
		if sn == "" {
			sn = fmt.Sprintf("?%d", gene.Sense)
		}
		cn := condNames[gene.Condition]
		if cn == "" {
			cn = fmt.Sprintf("?%d", gene.Condition)
		}
		an := actNames[gene.Action]
		if an == "" {
			an = fmt.Sprintf("?%d", gene.Action)
		}
		lines = append(lines, fmt.Sprintf("[%d] %s if %s -> %s (%.2f)", i+1, sn, cn, an, gene.Weight))
	}
	lines = append(lines, "")

	// Reserve space for the Family Tree button + footer
	const btnH = 16
	popH := padding*2 + len(lines)*lineH + btnH + lineH + padding

	// Background panel
	vector.FillRect(screen,
		float32(popX-padding), float32(popY-padding),
		float32(popW+padding*2), float32(popH),
		color.RGBA{0x10, 0x14, 0x28, 220}, false)

	// Colored title bar using cell's own color
	cr, cg, cb := c.ColorR, c.ColorG, c.ColorB
	vector.FillRect(screen,
		float32(popX-padding), float32(popY-padding),
		float32(popW+padding*2), float32(lineH+padding),
		color.RGBA{cr, cg, cb, 180}, false)

	// Border
	borderCol := color.RGBA{cr, cg, cb, 255}
	vector.FillRect(screen, float32(popX-padding), float32(popY-padding), float32(popW+padding*2), 1, borderCol, false)
	vector.FillRect(screen, float32(popX-padding), float32(popY-padding+popH), float32(popW+padding*2), 1, borderCol, false)
	vector.FillRect(screen, float32(popX-padding), float32(popY-padding), 1, float32(popH), borderCol, false)
	vector.FillRect(screen, float32(popX-padding+popW+padding*2), float32(popY-padding), 1, float32(popH), borderCol, false)

	// Text lines
	for i, line := range lines {
		ebitenutil.DebugPrintAt(screen, line, popX, popY+i*lineH)
	}

	// Family Tree button
	btnY := popY + len(lines)*lineH
	btnW := 100
	ftBtn := button{x: popX, y: btnY, w: btnW, h: btnH, label: "Family Tree"}
	g.familyTreeBtn = ftBtn
	drawButton(screen, ftBtn, g.showFamilyTree)

	// Footer
	ebitenutil.DebugPrintAt(screen, "[click elsewhere to close]", popX, btnY+btnH+2)
}

// drawCellPortrait renders a cell as it appears in the sim at a fixed screen position.
func drawCellPortrait(screen *ebiten.Image, cx, cy, radius float32, colorR, colorG, colorB uint8, defense float64, geneCount int) {
	borderThickness := float32(1.0 + defense*3.0)
	br, bg, bb := colorR, colorG, colorB
	if int(br)+50 < 256 { br += 50 } else { br = 255 }
	if int(bg)+50 < 256 { bg += 50 } else { bg = 255 }
	if int(bb)+50 < 256 { bb += 50 } else { bb = 255 }

	vector.FillCircle(screen, cx, cy, radius+borderThickness, color.RGBA{br, bg, bb, 255}, false)
	vector.FillCircle(screen, cx, cy, radius, color.RGBA{colorR, colorG, colorB, 255}, false)

	if geneCount >= 5 && radius >= 8 {
		dotR := radius * 0.22
		vector.FillCircle(screen, cx, cy, dotR, color.RGBA{br, bg, bb, 200}, false)
	}
	if geneCount >= 8 && radius >= 12 {
		off := radius * 0.45
		dotR := radius * 0.15
		dotCol := color.RGBA{br, bg, bb, 160}
		vector.FillCircle(screen, cx+off, cy, dotR, dotCol, false)
		vector.FillCircle(screen, cx-off, cy, dotR, dotCol, false)
	}
}

// drawFamilyTree renders the ancestry chain of a cell in a separate panel.
func (g *Game) drawFamilyTree(screen *ebiten.Image, c *Cell) {
	const (
		winX    = 10
		winY    = 70
		winW    = 210
		rowH    = 58
		padding = 10
		titleH  = 16
	)
	portCX := float32(winX + padding + 24)

	// Build entry list: ancestors oldest-first, then the current cell
	type entry struct {
		label  string
		colorR, colorG, colorB uint8
		size, defense float64
		geneCount     int
		isCurrent     bool
	}

	entries := make([]entry, 0, len(c.Ancestors)+1)
	for _, a := range c.Ancestors {
		entries = append(entries, entry{
			label:  fmt.Sprintf("Gen %d  #%d", a.Generation, a.ID),
			colorR: a.ColorR, colorG: a.ColorG, colorB: a.ColorB,
			size: a.Size, defense: a.Defense, geneCount: a.GeneCount,
		})
	}
	entries = append(entries, entry{
		label:  fmt.Sprintf("Gen %d  #%d  (selected)", c.Generation, c.ID),
		colorR: c.ColorR, colorG: c.ColorG, colorB: c.ColorB,
		size: c.Size, defense: c.Defense, geneCount: len(c.Genes),
		isCurrent: true,
	})

	winH := titleH + padding*2 + len(entries)*rowH

	// Background
	vector.FillRect(screen,
		float32(winX-padding), float32(winY-padding),
		float32(winW+padding*2), float32(winH),
		color.RGBA{0x10, 0x14, 0x28, 220}, false)

	// Border
	bCol := color.RGBA{0x60, 0x80, 0xa0, 255}
	vector.FillRect(screen, float32(winX-padding), float32(winY-padding), float32(winW+padding*2), 1, bCol, false)
	vector.FillRect(screen, float32(winX-padding), float32(winY-padding+winH), float32(winW+padding*2), 1, bCol, false)
	vector.FillRect(screen, float32(winX-padding), float32(winY-padding), 1, float32(winH), bCol, false)
	vector.FillRect(screen, float32(winX-padding+winW+padding*2), float32(winY-padding), 1, float32(winH), bCol, false)

	// Title
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Family Tree — #%d", c.ID), winX, winY)

	for i, e := range entries {
		cy := float32(winY+titleH+padding+i*rowH) + float32(rowH)/2

		// Portrait radius proportional to cell size
		r := float32(e.size * 4.5)
		if r < 8 { r = 8 }
		if r > 22 { r = 22 }
		if e.isCurrent { r = 26 }

		// Connector line down to next entry
		if i < len(entries)-1 {
			nextCY := float32(winY+titleH+padding+(i+1)*rowH) + float32(rowH)/2
			nextR := float32(entries[i+1].size * 4.5)
			if nextR < 8 { nextR = 8 }
			if nextR > 22 { nextR = 22 }
			if entries[i+1].isCurrent { nextR = 26 }
			lineTop := cy + r + 1
			lineBot := nextCY - nextR - 1
			if lineBot > lineTop {
				vector.FillRect(screen, portCX-1, lineTop, 2, lineBot-lineTop,
					color.RGBA{0x50, 0x60, 0x80, 200}, false)
			}
		}

		// Highlight glow for the selected (current) cell
		if e.isCurrent {
			vector.FillCircle(screen, portCX, cy, r+5, color.RGBA{0xff, 0xff, 0xee, 45}, false)
		}

		drawCellPortrait(screen, portCX, cy, r, e.colorR, e.colorG, e.colorB, e.defense, e.geneCount)

		// Label to the right
		textX := int(portCX) + int(r) + 8
		ebitenutil.DebugPrintAt(screen, e.label, textX, int(cy)-4)
		if e.isCurrent {
			ebitenutil.DebugPrintAt(screen, fmt.Sprintf("size %.1f  %d genes", e.size, e.geneCount), textX, int(cy)+8)
		}
	}
}

func (g *Game) drawGraph(screen *ebiten.Image) {
	const (
		graphX = 8
		graphY = 54
		graphW = 300
		graphH = 140
		pad    = 6
		legendH = 56
	)

	n := g.history.count
	if n < 2 {
		return
	}

	// Background
	vector.FillRect(screen,
		float32(graphX-pad), float32(graphY-pad),
		float32(graphW+pad*2), float32(graphH+pad*2+legendH),
		color.RGBA{0x08, 0x0c, 0x18, 210}, false)

	// Border
	bCol := color.RGBA{0x40, 0x60, 0x80, 200}
	vector.FillRect(screen, float32(graphX-pad), float32(graphY-pad), float32(graphW+pad*2), 1, bCol, false)
	vector.FillRect(screen, float32(graphX-pad), float32(graphY+graphH+pad+legendH), float32(graphW+pad*2), 1, bCol, false)
	vector.FillRect(screen, float32(graphX-pad), float32(graphY-pad), 1, float32(graphH+pad*2+legendH), bCol, false)
	vector.FillRect(screen, float32(graphX+graphW+pad), float32(graphY-pad), 1, float32(graphH+pad*2+legendH), bCol, false)

	// Find max population for scaling
	maxPop := 1
	for i := 0; i < n; i++ {
		s := g.history.get(i)
		if s.Population > maxPop {
			maxPop = s.Population
		}
	}
	// Round up to nice number
	scale := float64(maxPop) * 1.1
	if scale < 10 {
		scale = 10
	}

	// Helper to draw a line series
	drawLine := func(getValue func(HistorySample) float64, maxVal float64, col color.RGBA) {
		for i := 1; i < n; i++ {
			s0 := g.history.get(i - 1)
			s1 := g.history.get(i)
			v0 := getValue(s0) / maxVal
			v1 := getValue(s1) / maxVal
			if v0 > 1 { v0 = 1 }
			if v1 > 1 { v1 = 1 }

			x0 := float32(graphX) + float32(i-1)*float32(graphW)/float32(n-1)
			x1 := float32(graphX) + float32(i)*float32(graphW)/float32(n-1)
			y0 := float32(graphY+graphH) - float32(v0)*float32(graphH)
			y1 := float32(graphY+graphH) - float32(v1)*float32(graphH)

			// Draw as thin rect segments
			dx := x1 - x0
			dy := y1 - y0
			length := float32(math.Sqrt(float64(dx*dx + dy*dy)))
			if length < 0.5 {
				continue
			}
			// Approximate with horizontal rect + vertical offset
			vector.FillRect(screen, x0, min32(y0, y1), max32(dx, 1), max32(abs32(dy), 1)+1, col, false)
		}
	}

	// Draw series
	drawLine(func(s HistorySample) float64 { return float64(s.Population) }, scale, color.RGBA{0xff, 0xff, 0xff, 200}) // white: total
	drawLine(func(s HistorySample) float64 { return float64(s.Phototrophs) }, scale, color.RGBA{0x40, 0xd0, 0x40, 180}) // green: phototrophs
	drawLine(func(s HistorySample) float64 { return float64(s.Predators) }, scale, color.RGBA{0xe0, 0x50, 0x40, 180}) // red: predators
	drawLine(func(s HistorySample) float64 { return float64(s.Colonial) }, scale, color.RGBA{0x40, 0xb0, 0xff, 180}) // cyan: colonial

	// Legend below graph
	ly := graphY + graphH + pad + 2
	ebitenutil.DebugPrintAt(screen, "[G] Population Graph", graphX, ly)
	ly += 13
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("Total: %d  Max: %d", g.hudCells, maxPop), graphX, ly)
	ly += 13
	latest := g.history.get(n - 1)
	ebitenutil.DebugPrintAt(screen,
		fmt.Sprintf("Photo: %d  Pred: %d  Colony: %d",
			latest.Phototrophs, latest.Predators, latest.Colonial),
		graphX, ly)
	ly += 13

	// Color key
	vector.FillRect(screen, float32(graphX), float32(ly+2), 8, 8, color.RGBA{0xff, 0xff, 0xff, 200}, false)
	ebitenutil.DebugPrintAt(screen, "All", graphX+12, ly)
	vector.FillRect(screen, float32(graphX+40), float32(ly+2), 8, 8, color.RGBA{0x40, 0xd0, 0x40, 180}, false)
	ebitenutil.DebugPrintAt(screen, "Photo", graphX+52, ly)
	vector.FillRect(screen, float32(graphX+100), float32(ly+2), 8, 8, color.RGBA{0xe0, 0x50, 0x40, 180}, false)
	ebitenutil.DebugPrintAt(screen, "Pred", graphX+112, ly)
	vector.FillRect(screen, float32(graphX+160), float32(ly+2), 8, 8, color.RGBA{0x40, 0xb0, 0xff, 180}, false)
	ebitenutil.DebugPrintAt(screen, "Colony", graphX+172, ly)
}

func min32(a, b float32) float32 {
	if a < b { return a }
	return b
}
func max32(a, b float32) float32 {
	if a > b { return a }
	return b
}
func abs32(a float32) float32 {
	if a < 0 { return -a }
	return a
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
