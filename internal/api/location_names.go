package api

import (
	"strconv"
	"strings"
)

// Bulk location naming rules. Mirrored in web/src/utils/locationNames.ts —
// keep the two in sync; both sides are pinned by tests.

// spreadsheetLetters renders a 1-based index as spreadsheet column letters:
// 1->A ... 26->Z, 27->AA, 702->ZZ, 703->AAA (lowercase when lower).
func spreadsheetLetters(i int, lower bool) string {
	var out []byte
	for i > 0 {
		i--
		c := byte('A' + i%26)
		if lower {
			c = byte('a' + i%26)
		}
		out = append([]byte{c}, out...)
		i /= 26
	}
	return string(out)
}

// compactAxisLabel labels axis dim (0-based) at 1-based index i: dim0
// uppercase spreadsheet letters, dim1 plain numbers, dim2 lowercase
// spreadsheet letters. A single-dimension config uses plain numbers
// (there is no row/col distinction to make).
func compactAxisLabel(dim, i int, single bool) string {
	if single || dim == 1 {
		return strconv.Itoa(i)
	}
	if dim == 0 {
		return spreadsheetLetters(i, false)
	}
	return spreadsheetLetters(i, true)
}

// verboseAxisWord returns the default label word for axis dim: "Slot"
// for a single-dimension config's only axis (there is no row/col
// distinction to make), else "Row"/"Col"/"Bin" by position.
func verboseAxisWord(dim int, single bool) string {
	if single {
		return "Slot"
	}
	names := [3]string{"Row", "Col", "Bin"}
	if dim > 2 {
		dim = 2
	}
	return names[dim]
}

// verboseAxisLabel labels axis dim in words: the per-axis custom label
// from labels (when one is present and non-empty) followed by the 1-based
// index, else the default word ("Slot i" for a 1D config's only axis,
// "Row/Col/Bin" otherwise). labels is expected pre-trimmed; an empty
// entry means "use the default".
func verboseAxisLabel(dim, i int, single bool, labels []string) string {
	word := verboseAxisWord(dim, single)
	if dim < len(labels) && labels[dim] != "" {
		word = labels[dim]
	}
	return word + " " + strconv.Itoa(i)
}

// locationNodeName builds a generated location's name from its 1-based
// axis indices (one per dimension covered, axes 0..len-1). compact
// concatenates the axis labels ("A1", "A1c") and ignores labels; verbose
// space-joins them ("Row 3 Col 2 Bin 1"), substituting any non-empty
// per-axis label for the default word.
func locationNodeName(indices []int, naming string, single bool, labels []string) string {
	parts := make([]string, len(indices))
	for d, i := range indices {
		if naming == "verbose" {
			parts[d] = verboseAxisLabel(d, i, single, labels)
		} else {
			parts[d] = compactAxisLabel(d, i, single)
		}
	}
	if naming == "verbose" {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts, "")
}

// prefixedLeaf applies the optional prefix to a LEAF name only; a prefix
// never touches intermediate nested levels.
func prefixedLeaf(name, prefix string) string {
	if prefix == "" {
		return name
	}
	return prefix + " " + name
}

// bulkLocationNode is one generated location: its Name, its Path (joined
// 1-based axis indices, e.g. "1.2" - identifies the node), and the Parent
// path ("" = the new root location).
type bulkLocationNode struct {
	Name   string
	Path   string
	Parent string
}

// generateBulkLocations expands dims/structure/naming/prefix/labels into
// the ordered list of nodes to create under a fresh root. flat: one leaf
// per combination, all direct children of the root, dim0 iterating
// outermost so names sort naturally. nested: one level per dimension; a
// node at depth d is parented to its axes-0..d-1 node (depth-first order
// keeps every parent ahead of its children). The prefix, when set,
// prepends to leaf names only. labels (verbose naming only) replaces the
// default per-axis word; empty entries and nil keep the defaults.
func generateBulkLocations(dims []int, structure, naming, prefix string, labels []string) []bulkLocationNode {
	single := len(dims) == 1
	out := []bulkLocationNode{}
	idx := make([]int, len(dims))
	if structure == "flat" {
		total := 1
		for _, d := range dims {
			total *= d
		}
		for n := 0; n < total; n++ { // last dim varies fastest
			rem := n
			path := make([]string, len(dims))
			for d := len(dims) - 1; d >= 0; d-- {
				idx[d] = rem%dims[d] + 1
				path[d] = strconv.Itoa(idx[d])
				rem /= dims[d]
			}
			out = append(out, bulkLocationNode{
				Name:   prefixedLeaf(locationNodeName(idx, naming, single, labels), prefix),
				Path:   strings.Join(path, "."),
				Parent: "",
			})
		}
		return out
	}
	var rec func(depth int, parentPath string)
	rec = func(depth int, parentPath string) {
		leaf := depth == len(dims)-1
		for i := 1; i <= dims[depth]; i++ {
			idx[depth] = i
			path := strconv.Itoa(i)
			if parentPath != "" {
				path = parentPath + "." + path
			}
			name := locationNodeName(idx[:depth+1], naming, single, labels)
			if leaf {
				name = prefixedLeaf(name, prefix)
			}
			out = append(out, bulkLocationNode{Name: name, Path: path, Parent: parentPath})
			if !leaf {
				rec(depth+1, path)
			}
		}
	}
	rec(0, "")
	return out
}
