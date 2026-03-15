package compiler

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Single-row DP: prev holds the previous row, curr the current.
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := range len(a) {
		curr[0] = i + 1
		for j := range len(b) {
			cost := 1
			if a[i] == b[j] {
				cost = 0
			}
			ins := curr[j] + 1
			del := prev[j+1] + 1
			sub := prev[j] + cost
			curr[j+1] = min(ins, min(del, sub))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// closestMatch finds the candidate closest to input by edit distance.
// Returns "" if no candidate is close enough.
func closestMatch(input string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	threshold := len(input) / 3
	if threshold < 2 {
		threshold = 2
	}

	best := ""
	bestDist := threshold + 1
	for _, c := range candidates {
		d := levenshtein(input, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// collectKeys returns the keys of a map as a string slice.
func collectKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// suggest returns "; did you mean <name>?" if a close match exists, else "".
func suggest(input string, candidates []string) string {
	if m := closestMatch(input, candidates); m != "" {
		return "; did you mean " + m + "?"
	}
	return ""
}
