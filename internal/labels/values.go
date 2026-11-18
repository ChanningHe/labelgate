package labels

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// collector accumulates label errors.
type collector struct {
	errs []model.LabelError
}

func (c *collector) add(key, format string, args ...any) {
	c.errs = append(c.errs, model.LabelError{Key: key, Message: fmt.Sprintf(format, args...)})
}

// addUnknown reports an unknown property (nothing was dropped because of it).
func (c *collector) addUnknown(key, name string) {
	c.errs = append(c.errs, model.LabelError{Key: key, Message: fmt.Sprintf("unknown property %q, ignored; check the spelling against the label reference", name), Unknown: true})
}

// sorted returns the errors ordered by key, then message, without duplicates.
func (c *collector) sorted() []model.LabelError {
	if len(c.errs) == 0 {
		return nil
	}
	out := append([]model.LabelError(nil), c.errs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Message < out[j].Message
	})
	uniq := out[:1]
	for _, e := range out[1:] {
		if e != uniq[len(uniq)-1] {
			uniq = append(uniq, e)
		}
	}
	return uniq
}

// sortedKeys returns the map keys in ascending order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q (use true or false)", s)
}

func parseInt(s string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", s)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("value %d out of range %d-%d", n, lo, hi)
	}
	return n, nil
}

// parseSeconds parses a whole-second duration: a Go duration ("30s", "2m")
// or a plain number of seconds ("30").
func parseSeconds(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	d, err := time.ParseDuration(s)
	if err != nil {
		n, convErr := strconv.Atoi(s)
		if convErr != nil {
			return 0, fmt.Errorf("invalid duration %q (use 30s or 30)", s)
		}
		d = time.Duration(n) * time.Second
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q must not be negative", s)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("duration %q must be a whole number of seconds", s)
	}
	return d, nil
}

// splitList splits a comma-separated list, trimming blanks and dropping empties.
func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
