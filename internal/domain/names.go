package domain

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// CleanName trims and collapses internal whitespace, so "  Interview   Tracker " is stored
// and compared as "Interview Tracker".
func CleanName(raw string) string {
	return strings.Join(strings.Fields(raw), " ")
}

// SameName compares two names the way the database unique indexes do: whitespace-normalised
// and case-insensitive.
func SameName(a, b string) bool {
	return strings.EqualFold(CleanName(a), CleanName(b))
}

// ValidateName cleans a user supplied name and checks its length. label names the thing
// being validated in error messages ("tracker name", "column name").
func ValidateName(label, raw string, maxLength int) (string, error) {
	name := CleanName(raw)
	if name == "" {
		return "", InvalidInputf("The %s can't be empty.", label)
	}
	if utf8.RuneCountInString(name) > maxLength {
		return "", InvalidInputf("The %s %q is too long (max %d characters).", label, name, maxLength)
	}
	return name, nil
}

// SuggestSimilar returns up to limit candidates that look like a misspelling of input,
// closest first. It is used for column names, select options, operators and types.
func SuggestSimilar(input string, candidates []string, limit int) []string {
	needle := strings.ToLower(CleanName(input))
	if needle == "" {
		return nil
	}
	type scored struct {
		value    string
		distance int
	}
	var matches []scored
	for _, candidate := range candidates {
		hay := strings.ToLower(candidate)
		distance := levenshtein(needle, hay)
		tolerance := max(2, utf8.RuneCountInString(needle)/3)
		isSubstring := strings.Contains(hay, needle) || strings.Contains(needle, hay)
		if distance <= tolerance || isSubstring {
			matches = append(matches, scored{value: candidate, distance: distance})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].distance < matches[j].distance })
	result := make([]string, 0, min(limit, len(matches)))
	for i := 0; i < len(matches) && i < limit; i++ {
		result = append(result, matches[i].value)
	}
	return result
}

// levenshtein computes the edit distance between two strings, rune aware.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			substitutionCost := 1
			if ra[i-1] == rb[j-1] {
				substitutionCost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+substitutionCost)
		}
		previous, current = current, previous
	}
	return previous[len(rb)]
}

// QuoteAll wraps each value in double quotes, for readable lists in messages.
func QuoteAll(values []string) []string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = `"` + v + `"`
	}
	return quoted
}
