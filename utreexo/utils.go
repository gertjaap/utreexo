package utreexo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// "verbose" is a global const to get lots of printfs for debugging
var verbose = false

// DedupeHashSlices is for removing txos that get created & spent in the same block
// as adds are TTLHashes, takes those in for slice a
func DedupeHashSlices(as *[]LeafTXO, bs *[]Hash) {
	// need to preserve order, so have to do this twice...
	// build a map and b map
	ma := make(map[Hash]bool)
	for _, a := range *as {
		ma[a.Hash] = true
	}
	mb := make(map[Hash]bool)
	for _, b := range *bs {
		mb[b] = true
	}
	var anew []LeafTXO
	var bnew []Hash

	for _, a := range *as {
		_, there := mb[a.Hash]
		if !there {
			anew = append(anew, a)
		}
	}
	for _, b := range *bs {
		_, there := ma[b]
		if !there {
			bnew = append(bnew, b)
		}
	}
	*as = anew
	*bs = bnew
}

// Popcount returns the number of 1 bits in a uint64
func PopCount(i uint64) uint8 {
	var count uint8
	for j := 0; j < 64; j++ {
		if i&1 == 1 {
			count++
		}
		i >>= 1
	}
	return count
}

// ExtractTwins takes a slice of ints and extracts the adjacent ints
// which differ only in the LSB.  It then returns two slices: one of the
// *even* twins (no odds), and one of the ints with no siblings
func ExtractTwins(nodes []uint64) (twins, onlychildren []uint64) {
	// "twins" are siblings where both are deleted (I guess)

	// run through the slice of deletions, and 'dedupe' by extracting siblings
	// (if both siblings are being deleted, nothing needs to move on that row)
	for i := 0; i < len(nodes); i++ {
		if i+1 < len(nodes) && nodes[i]|1 == nodes[i+1] {
			twins = append(twins, nodes[i])
			i++ // skip one here
		} else {
			onlychildren = append(onlychildren, nodes[i])
		}
	}
	return
}

// tree height 0 means there's 1 lead.  Tree height 1 means 2 leaves.
// so it's 1<<height leaves.  ... pretty sure about this

// detectSubTreeHight finds the height of the subtree a given LEAF position and
// the number of leaves (& the forest height which is redundant)
// This thing is a tricky one.  Makes a weird serpinski fractal thing if
// you map it out in a table.
// Oh wait it's pretty simple.  Go left to right through the bits of numLeaves,
// and subtract that from position until it goes negative.
// (Does not work for nodes not at the bottom)
func detectSubTreeHeight(
	position uint64, numLeaves uint64, forestHeight uint8) (h uint8) {
	for h = forestHeight; position >= (1<<h)&numLeaves; h-- {
		position -= (1 << h) & numLeaves
	}
	return
}

// detectHeight finds the current height of your node given the node
// position and the total forest height.. counts preceeding 1 bits.
func detectHeight(position uint64, forestHeight uint8) uint8 {
	marker := uint64(1 << forestHeight)
	var h uint8
	for h = 0; position&marker != 0; h++ {
		marker >>= 1
	}
	return h
}

// detectOffset takes a node position and number of leaves in forest, and
// returns: which subtree a node is in, the bitfield to get from the top to node,
// and the height from node to tree top (length of that bitfield).
func detectOffset(position uint64, numLeaves uint64) (uint8, uint64, uint8) {
	// TODO replace ?
	// similarities to detectSubTreeHeight() with more features
	// maybe replace detectSubTreeHeight with this
	th := treeHeight(numLeaves)
	nh := detectHeight(position, th) // there's probably a fancier way with bits...

	var biggerTrees uint8

	// add trees until you would exceed position of node
	// TODO write some description of this bitshift stuff.  Bit of a mess.
	for ; (position<<nh)&((2<<th)-1) >= (1<<th)&numLeaves; th-- {
		treeSize := (1 << th) & numLeaves
		if treeSize != 0 {
			position -= treeSize
			biggerTrees++
		}
	}
	return biggerTrees, position, th - nh
}

// child gives you the left child (LSB will be 0)
func child(position uint64, forestHeight uint8) uint64 {
	mask := uint64(2<<forestHeight) - 1
	return (position << 1) & mask
}

// go down drop times (always left; LSBs will be 0) and return position
func childMany(position uint64, drop, forestHeight uint8) uint64 {
	mask := uint64(2<<forestHeight) - 1
	return (position << drop) & mask
}

// Return the position of the parent of this position
func up1(position uint64, forestHeight uint8) uint64 {
	return (position >> 1) | (1 << forestHeight)
}

// go up rise times and return the position
func upMany(position uint64, rise, forestHeight uint8) uint64 {
	mask := uint64(2<<forestHeight) - 1
	return (position>>rise | (mask << uint64(forestHeight-(rise-1)))) & mask
}

// cousin returns a cousin: the child of the parent's sibling.
// you just xor with 2.  Actually there's no point in calling this function but
// it's here to document it.  If you're the left sibling it returns the left
// cousin.
func cousin(position uint64) uint64 {
	return position ^ 2
}

// check if a node is in a forest based on number of leaves.
// go down and right until reaching the bottom, then check if over numleaves
// (same as childmany)
func inForest(pos, numLeaves uint64) bool {
	// quick yes:
	if pos < numLeaves {
		return true
	}

	h := treeHeight(numLeaves)
	marker := uint64(1 << h)
	mask := (marker << 1) - 1
	if pos >= mask {
		return false
	}
	for pos&marker != 0 {
		pos = ((pos << 1) & mask) | 1
	}
	return pos < numLeaves
}

// given n leaves, how deep is the tree?
// iterate shifting left until greater than n
func treeHeight(n uint64) uint8 {
	var e uint8
	for ; (1 << e) < n; e++ {
	}
	return e
}

// getTops gives you the positions of the tree tops, given a number of leaves.
// LOWEST first (right to left) (blarg change this)
func getTopsReverse(leaves uint64, forestHeight uint8) (tops []uint64, heights []uint8) {
	position := uint64(0)

	// go left to right.  But append in reverse so that the tops are low to high
	// run though all bit positions.  if there's a 1, build a tree atop
	// the current position, and move to the right.
	for height := forestHeight; position < leaves; height-- {
		if (1<<height)&leaves != 0 {
			// build a tree here
			top := upMany(position, height, forestHeight)
			tops = append([]uint64{top}, tops...)
			heights = append([]uint8{height}, heights...)
			position += 1 << height
		}
	}
	return
}

// subTreePositions takes in a node position and forestHeight and returns the
// positions of all children that need to move AND THE NODE ITSELF.  (it works nicer that way)
// Also it returns where they should move to, given the destination of the
// sub-tree root.
// can also be used with the "to" return discarded to just enumerate a subtree
// swap tells whether to activate the sibling swap to try to preserve order
func subTreePositions(
	subroot uint64, moveTo uint64, forestHeight uint8) (m []move) {

	subHeight := detectHeight(subroot, forestHeight)
	//	fmt.Printf("node %d height %d\n", subroot, subHeight)
	rootDelta := int64(moveTo) - int64(subroot)
	// do this with nested loops instead of recursion ... because that's
	// more fun.
	// h is out height in the forest
	// start at the bottom and ascend
	for height := uint8(0); height <= subHeight; height++ {
		// find leftmost child at this height; also calculate the
		// delta (movement) for this row
		depth := subHeight - height
		leftmost := childMany(subroot, depth, forestHeight)
		rowDelta := rootDelta << depth // usually negative
		for i := uint64(0); i < 1<<depth; i++ {
			// loop left to right
			f := leftmost + i
			t := uint64(int64(f) + rowDelta)
			m = append(m, move{from: f, to: t})
		}
	}

	return
}

// subTreeLeafRange gives the range of leaves under a node
func subTreeLeafRange(
	subroot uint64, forestHeight uint8) (uint64, uint64) {

	h := detectHeight(subroot, forestHeight)
	left := childMany(subroot, h, forestHeight)
	run := uint64(1 << h)

	return left, run
}

// it'd be cool if you just had .sort() methods on slices of builtin types...
func sortUint64s(s []uint64) {
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
}

func sortNodeSlice(s []Node) {
	sort.Slice(s, func(a, b int) bool { return s[a].Pos < s[b].Pos })
}

// sortMoves sorts them by from, not to
func sortMoves(s []move) {
	sort.Slice(s, func(a, b int) bool { return s[a].from < s[b].from })
}

// mergeSortedSlices takes two slices (of uint64s; though this seems
// genericizable in that it's just < and > operators) and merges them into
// a signle sorted slice, discarding duplicates.
// (eg [1, 5, 8, 9], [2, 3, 4, 5, 6] -> [1, 2, 3, 4, 5, 6, 8, 9]
func mergeSortedSlices(a []uint64, b []uint64) (c []uint64) {
	maxa := len(a)
	maxb := len(b)

	// make it (potentially) too long and truncate later
	c = make([]uint64, maxa+maxb)

	idxa, idxb := 0, 0
	for j := 0; j < len(c); j++ {
		// if we're out of a or b, just use the remainder of the other one
		if idxa >= maxa {
			// a is done, copy remainder of b
			j += copy(c[j:], b[idxb:])
			c = c[:j] // truncate empty section of c
			break
		}
		if idxb >= maxb {
			// b is done, copy remainder of a
			j += copy(c[j:], a[idxa:])
			c = c[:j] // truncate empty section of c
			break
		}

		vala, valb := a[idxa], b[idxb]
		if vala < valb { // a is less so append that
			c[j] = vala
			idxa++
		} else if vala > valb { // b is less so append that
			c[j] = valb
			idxb++
		} else { // they're equal
			c[j] = vala
			idxa++
			idxb++
		}
	}
	return
}

// ToString prints out the whole thing.  Only viable for small forests
func BinString(leaves uint64) string {
	fh := treeHeight(leaves)

	// tree height should be 6 or less
	if fh > 6 {
		return "forest too big to print "
	}

	output := make([]string, (fh*2)+1)
	var pos uint8
	for h := uint8(0); h <= fh; h++ {
		rowlen := uint8(1 << (fh - h))

		for j := uint8(0); j < rowlen; j++ {
			//			if pos < uint8(leaves) {
			output[h*2] += fmt.Sprintf("%05b ", pos)
			//			} else {
			//				output[h*2] += fmt.Sprintf("       ")
			//			}

			if h > 0 {
				//				if x%2 == 0 {
				output[(h*2)-1] += "|-----"
				for q := uint8(0); q < ((1<<h)-1)/2; q++ {
					output[(h*2)-1] += "------"
				}
				output[(h*2)-1] += "\\     "
				for q := uint8(0); q < ((1<<h)-1)/2; q++ {
					output[(h*2)-1] += "      "
				}

				//				}

				for q := uint8(0); q < (1<<h)-1; q++ {
					output[h*2] += "      "
				}

			}
			pos++
		}

	}
	var s string
	for z := len(output) - 1; z >= 0; z-- {
		s += output[z] + "\n"
	}
	return s
}

// 4 byte slice to uint32.  Returns ffffffff if something doesn't work.
func BtU32(b []byte) uint32 {
	if len(b) != 4 {
		fmt.Printf("Got %x to BtU32 (%d bytes)\n", b, len(b))
		return 0xffffffff
	}
	var i uint32
	buf := bytes.NewBuffer(b)
	binary.Read(buf, binary.BigEndian, &i)
	return i
}

// uint32 to 4 bytes.  Always works.
func U32tB(i uint32) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, i)
	return buf.Bytes()
}

// 8 bytes to uint64.  returns ffff. if it doesn't work.
func BtU64(b []byte) uint64 {
	if len(b) != 8 {
		fmt.Printf("Got %x to BtU64 (%d bytes)\n", b, len(b))
		return 0xffffffffffffffff
	}
	var i uint64
	buf := bytes.NewBuffer(b)
	binary.Read(buf, binary.BigEndian, &i)
	return i
}

// uint64 to 8 bytes.  Always works.
func U64tB(i uint64) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, i)
	return buf.Bytes()
}

// ResolveNode will try to get a node at a given pos from a known set of nodes/leaves
// if not present, it will descend to calculate it from known data further down.
// should optimize this to return all the hashes it calculated so the caller can add it
// to its cache/knownData
func ResolveNode(knownData map[uint64]Hash, pos uint64, forestHeight uint8) (Hash, map[uint64]Hash, error) {
	calculatedHashes := map[uint64]Hash{}
	// First check if the node is in here
	n, ok := knownData[pos]
	if ok {
		return n, calculatedHashes, nil
	}

	// If not, check if we're at a leaf. If so, we can't fix this.
	if detectHeight(pos, forestHeight) == 0 {
		return Hash{}, calculatedHashes, fmt.Errorf("Could not find leaf %d", pos)
	}

	// Otherwise, find if the children are known and hash them
	leftPos := child(pos, forestHeight)
	rightPos := leftPos ^ 1

	left, leftHashes, err := ResolveNode(knownData, leftPos, forestHeight)
	for pos, hash := range leftHashes {
		calculatedHashes[pos] = hash
	}

	if err != nil {
		return Hash{}, calculatedHashes, err
	}

	right, rightHashes, err := ResolveNode(knownData, rightPos, forestHeight)
	for pos, hash := range rightHashes {
		calculatedHashes[pos] = hash
	}

	if err != nil {
		return Hash{}, calculatedHashes, err
	}
	calculatedHashes[pos] = Parent(left, right)
	return calculatedHashes[pos], calculatedHashes, nil
}
