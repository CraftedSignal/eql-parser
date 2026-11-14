package eql

import (
	"context"
	"errors"
	"fmt"
)

const (
	// MaxTokens bounds token storage independently of the 1 MiB text limit.
	MaxTokens = 32 * 1024
	// MaxParseNodes bounds AST and extracted expression nodes across one call.
	MaxParseNodes = 32 * 1024
	// MaxConditions includes conditions repeated in sequence-step subqueries.
	MaxConditions = 4096
	// MaxOutputBytes bounds accumulated extracted text and structural overhead.
	MaxOutputBytes = 4 * 1024 * 1024
	maxParseWork   = 4 * 1024 * 1024
)

// ErrResourceLimit indicates that a query exceeded a parsing resource budget.
var ErrResourceLimit = errors.New("query exceeds parsing resource limits")

// parseBudget belongs to one synchronous call and is shared by every phase
// and sequence subquery. A private abort unwinds immediately through helpers
// that do not return errors; public entry points turn it into a normal error.
type parseBudget struct {
	ctx        context.Context
	work       int
	tokens     int
	nodes      int
	conditions int
	output     int
}

type parseAbort struct{ err error }

func newParseBudget(ctx context.Context) *parseBudget {
	return &parseBudget{ctx: ctx, work: maxParseWork, tokens: MaxTokens, nodes: MaxParseNodes, conditions: MaxConditions, output: MaxOutputBytes}
}

func (b *parseBudget) check(work int) {
	if b == nil {
		return
	}
	if err := b.ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			panic(parseAbort{fmt.Errorf("parser timeout: %w", err)})
		}
		panic(parseAbort{err})
	}
	b.work -= work
	if b.work < 0 {
		b.fail("work")
	}
}

func (b *parseBudget) fail(resource string) {
	panic(parseAbort{fmt.Errorf("%w (%s)", ErrResourceLimit, resource)})
}

func (b *parseBudget) token() {
	if b == nil {
		return
	}
	b.check(1)
	b.tokens--
	if b.tokens < 0 {
		b.fail("tokens")
	}
}

func (b *parseBudget) node() {
	if b == nil {
		return
	}
	b.check(1)
	b.nodes--
	if b.nodes < 0 {
		b.fail("nodes")
	}
	b.addOutput(0)
}

func (b *parseBudget) addOutput(textBytes int) {
	if b == nil {
		return
	}
	b.check(1)
	// Six bytes covers JSON escaping of one input byte; fixed overhead
	// covers the containing result entry and its scalar metadata.
	b.output -= 128 + 6*textBytes
	if b.output < 0 {
		b.fail("output")
	}
}

func (b *parseBudget) condition() {
	if b == nil {
		return
	}
	b.check(1)
	b.conditions--
	if b.conditions < 0 {
		b.fail("conditions")
	}
}

func recoveredParseError(value any) error {
	if aborted, ok := value.(parseAbort); ok {
		return aborted.err
	}
	panic(value)
}
