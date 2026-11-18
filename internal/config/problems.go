package config

import (
	"fmt"
	"strings"
)

// ValidationErrors is the error returned when a configuration has several
// problems. Every element is a *ValidationError, so errors.As finds the first
// one and callers can iterate all of them. A single problem is never wrapped:
// it is returned as a bare *ValidationError.
type ValidationErrors []*ValidationError

// Error lists one problem per line.
func (p ValidationErrors) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %d problems:", len(p))
	for _, ve := range p {
		b.WriteString("\n  " + KeyWithEnv(ve.Field) + ": " + ve.Message)
	}
	return b.String()
}

// Unwrap exposes every problem to errors.Is and errors.As.
func (p ValidationErrors) Unwrap() []error {
	errs := make([]error, len(p))
	for i, ve := range p {
		errs[i] = ve
	}
	return errs
}

// add appends ve unless it is nil, so checks can add their result directly.
func (p *ValidationErrors) add(ve *ValidationError) {
	if ve != nil {
		*p = append(*p, ve)
	}
}

// addf appends a problem for field.
func (p *ValidationErrors) addf(field, format string, args ...any) {
	p.add(fail(field, format, args...))
}

// err returns nil, the only problem, or all problems.
func (p ValidationErrors) err() error {
	switch len(p) {
	case 0:
		return nil
	case 1:
		return p[0]
	}
	return p
}

// fail builds a *ValidationError.
func fail(field, format string, args ...any) *ValidationError {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}
