package compiler

import (
	"strconv"
	"strings"
)

func (p *parser) parseAssertStmt(ctx *parseContext, comment string, assertPos int) (*AssertStmt, error) {
	// Record source file and line for the notify text.
	file := p.sourcePath
	if file == "" {
		file = "<input>"
	}
	line, _ := p.posToLineCol(assertPos)

	stmt := &AssertStmt{
		File:    file,
		Line:    line,
		Comment: comment,
	}

	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	if peek.kind == tokLParen {
		// Parenthesized form: parse arguments inside parens, then check for block.
		return p.parseAssertParens(ctx, stmt, peek.pos)
	}

	// No-parens disambiguation:
	// 1. If next is `{` → block form, no pre-block args.
	if peek.kind == tokLBrace {
		return p.parseAssertBlock(ctx, stmt, peek.pos)
	}

	// 2. If next is a string literal → message. Parse optional `, value: expr`. Require `{`.
	if peek.kind == tokString {
		stmt.Message = peek.val
		if err := p.parseAssertKwArgs(ctx, stmt); err != nil {
			return nil, err
		}
		brace, err := p.next()
		if err != nil {
			return nil, err
		}
		if brace.kind != tokLBrace {
			return nil, p.errorf(brace.pos, "assert with message but no condition — add a condition expression or use block form")
		}
		return p.parseAssertBlock(ctx, stmt, brace.pos)
	}

	// 3. If next is identifier `value` followed by `:` → keyword arg, no message.
	if peek.kind == tokIdent && peek.val == "value" {
		saved := p.save()
		colon, err := p.next()
		if err != nil {
			return nil, err
		}
		if colon.kind == tokColon {
			valExpr, err := p.parseBoolExpr(ctx.resolve)
			if err != nil {
				return nil, err
			}
			if truthy, ok := valExpr.(*TruthyExpr); ok {
				valExpr = truthy.Value
			}
			stmt.Value = valExpr
			brace, err := p.next()
			if err != nil {
				return nil, err
			}
			if brace.kind != tokLBrace {
				return nil, p.errorf(brace.pos, "assert with value but no condition — add a condition expression or use block form")
			}
			return p.parseAssertBlock(ctx, stmt, brace.pos)
		}
		// Not `value:`, restore and fall through to expression form.
		p.restore(saved)
	}

	// 4. Otherwise → expression form. Parse condition expr.
	p.unget(peek)
	condStart := peek.pos
	cond, err := p.parseBoolExpr(ctx.resolve)
	if err != nil {
		return nil, err
	}
	// Capture source text of condition: from the start of the first condition
	// token to the position of the next peeked token.
	condEnd := p.pos
	if p.ungot != nil {
		condEnd = p.ungot.pos
	}
	stmt.ConditionText = strings.TrimSpace(p.src[condStart:condEnd])
	stmt.Condition = cond

	// Parse optional trailing `, "message"` and/or `, value: <expr>`.
	if err := p.parseAssertTrailingArgs(ctx, stmt); err != nil {
		return nil, err
	}

	return stmt, nil
}

// parseAssertBlock parses the block body of an assert statement.
// The opening `{` has already been consumed.
func (p *parser) parseAssertBlock(ctx *parseContext, stmt *AssertStmt, bracePos int) (*AssertStmt, error) {
	stmts, err := ctx.parseBody(true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(bracePos, "empty assert block")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(bracePos, "last item in assert block must be a condition expression")
	}
	stmt.Body = stmts[:len(stmts)-1]

	// Capture condition text for the tail expression.
	// For block form, we use the expression node's string representation.
	stmt.Condition = tail.Expr
	// ConditionText for block form: we don't have byte offsets for the tail
	// inside the block, so use the message if available, otherwise a generic text.
	if stmt.ConditionText == "" {
		stmt.ConditionText = "assertion"
	}

	return stmt, nil
}

// parseAssertKwArgs parses optional `, value: <expr>` after a message string.
func (p *parser) parseAssertKwArgs(ctx *parseContext, stmt *AssertStmt) error {
	peek, err := p.next()
	if err != nil {
		return err
	}
	if peek.kind != tokComma {
		p.unget(peek)
		return nil
	}
	// Expect `value:`
	kw, err := p.next()
	if err != nil {
		return err
	}
	if kw.kind != tokIdent || kw.val != "value" {
		return p.errorf(kw.pos, "expected 'value' keyword argument, got %s", kw.describe())
	}
	if _, err := p.expect(tokColon); err != nil {
		return err
	}
	valExpr, err := p.parseBoolExpr(ctx.resolve)
	if err != nil {
		return err
	}
	if truthy, ok := valExpr.(*TruthyExpr); ok {
		valExpr = truthy.Value
	}
	stmt.Value = valExpr
	return nil
}

// parseAssertTrailingArgs parses optional `, "message"` and/or `, value: <expr>`
// after the condition in expression form.
func (p *parser) parseAssertTrailingArgs(ctx *parseContext, stmt *AssertStmt) error {
	for {
		peek, err := p.next()
		if err != nil {
			return err
		}
		if peek.kind != tokComma {
			p.unget(peek)
			return nil
		}

		next, err := p.next()
		if err != nil {
			return err
		}

		// String literal → message
		if next.kind == tokString {
			stmt.Message = next.val
			continue
		}

		// `value:` → keyword argument
		if next.kind == tokIdent && next.val == "value" {
			colon, err := p.next()
			if err != nil {
				return err
			}
			if colon.kind != tokColon {
				return p.errorf(colon.pos, "expected ':' after 'value'")
			}
			valExpr, err := p.parseBoolExpr(ctx.resolve)
			if err != nil {
				return err
			}
			if truthy, ok := valExpr.(*TruthyExpr); ok {
				valExpr = truthy.Value
			}
			stmt.Value = valExpr
			continue
		}

		return p.errorf(next.pos, "unexpected %s in assert arguments — expected string message or 'value:' keyword", next.describe())
	}
}

// parseAssertParens parses a parenthesized assert statement.
// The opening `(` has already been consumed.
func (p *parser) parseAssertParens(ctx *parseContext, stmt *AssertStmt, lparenPos int) (*AssertStmt, error) {
	// Parse arguments inside parens: positional args and optional value: keyword.
	var positionalArgs []token // track kind/val for reinterpretation
	var positionalExprs []Expr
	var hasValue bool

	for {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			break
		}
		if len(positionalExprs) > 0 || hasValue {
			// Expect comma between args
			if peek.kind != tokComma {
				return nil, p.errorf(peek.pos, "expected ',' or ')' in assert arguments")
			}
			peek, err = p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokRParen {
				break
			}
		}

		// Check for `value:` keyword arg
		if peek.kind == tokIdent && peek.val == "value" {
			saved := p.save()
			colon, err := p.next()
			if err != nil {
				return nil, err
			}
			if colon.kind == tokColon {
				valExpr, err := p.parseBoolExpr(ctx.resolve)
				if err != nil {
					return nil, err
				}
				if truthy, ok := valExpr.(*TruthyExpr); ok {
					valExpr = truthy.Value
				}
				stmt.Value = valExpr
				hasValue = true
				continue
			}
			p.restore(saved)
		}

		// Parse as expression (could be string literal or condition)
		p.unget(peek)
		condStart := p.pos
		if p.ungot != nil {
			condStart = p.ungot.pos
		}
		expr, err := p.parseBoolExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		condEnd := p.pos
		if p.ungot != nil {
			condEnd = p.ungot.pos
		}

		positionalArgs = append(positionalArgs, peek)
		positionalExprs = append(positionalExprs, expr)
		// Store condition text for first non-string positional
		if peek.kind != tokString && stmt.ConditionText == "" {
			stmt.ConditionText = strings.TrimSpace(p.src[condStart:condEnd])
		}
	}

	// Check for block form after `)`
	blockPeek, err := p.next()
	if err != nil {
		return nil, err
	}

	if blockPeek.kind == tokLBrace {
		// Block form: positional args are message (strings only)
		for i, arg := range positionalArgs {
			if arg.kind == tokString {
				stmt.Message = arg.val
			} else {
				_ = positionalExprs[i] // suppress unused
				return nil, p.errorf(arg.pos, "assert block form only accepts a string message as positional argument, got %s", arg.describe())
			}
		}
		return p.parseAssertBlock(ctx, stmt, blockPeek.pos)
	}

	// Expression form: first positional = condition (error if string literal)
	p.unget(blockPeek)

	if len(positionalExprs) == 0 {
		return nil, p.errorf(lparenPos, "assert requires a condition expression")
	}

	if positionalArgs[0].kind == tokString {
		return nil, p.errorf(positionalArgs[0].pos, "string literal cannot be used as assert condition")
	}

	stmt.Condition = positionalExprs[0]

	// Second positional string = message
	for i := 1; i < len(positionalExprs); i++ {
		if positionalArgs[i].kind == tokString {
			stmt.Message = positionalArgs[i].val
		} else {
			return nil, p.errorf(positionalArgs[i].pos, "unexpected positional argument in assert — only condition and message string are allowed")
		}
	}

	return stmt, nil
}

// emitAssertStmt emits an assert statement. The condition is checked with a
// boolean check frame. The true branch (condition met) falls through to
// continuation. The false branch (assertion failed) emits a notify + exit pair.
func (p *parser) emitAssertStmt(s *AssertStmt, ctx *emitContext, comment string) error {
	// Step 1: If value expr present, resolve it first (may emit frames).
	var val any
	if s.Value != nil {
		var err error
		val, err = ctx.exprGetValue(s.Value, "")
		if err != nil {
			return err
		}
	}

	// Step 2: If block form, emit the body statements first.
	if s.Body != nil {
		ctx.pushScope()
		if err := ctx.emitBody(s.Body); err != nil {
			ctx.popScope()
			return err
		}
		ctx.popScope()
	}

	// Step 3: Resolve the boolean condition.
	resolved, err := ctx.resolveBool(s.Condition)
	if err != nil {
		return err
	}

	// Step 4: Compute frame positions.
	checkStart := ctx.b.pos()
	checkCount := resolved.frameCount()
	// Error handler is always exactly 2 frames (notify + exit)
	// since value was pre-resolved above.
	trueBranch := frameRef(checkStart + checkCount + 2) // skip notify+exit
	falseBranch := frameRef(checkStart + checkCount)    // fall into notify

	// Step 5: Emit check frames. For assert, TRUE means condition holds
	// (skip error handler), FALSE means assertion failed (fall into notify).
	if resolved.isLeaf() {
		p.emitBoolCheckFrame(resolved.term, trueBranch, falseBranch, ctx.b, comment)
	} else {
		p.emitResolvedBoolFrames(resolved, trueBranch, falseBranch, ctx.b, comment)
	}
	stripFallThrough(ctx.b, checkStart, checkCount)

	// Step 6: Build notify text and emit notify frame.
	message := s.Message
	if message == "" {
		message = "Assertion failed: " + s.ConditionText
	}
	notifyText := s.File + ":" + strconv.Itoa(s.Line) + " " + message
	notifyFrame := map[string]any{
		"op":  "notify",
		"txt": notifyText,
	}
	if val != nil {
		notifyFrame["0"] = val
	}
	ctx.b.emit(notifyFrame)

	// Step 7: Emit exit frame.
	ctx.b.emit(map[string]any{"op": "exit"})

	return nil
}
