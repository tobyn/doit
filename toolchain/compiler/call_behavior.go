package compiler

import (
	"fmt"
	"maps"
	"path"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/syntax"
)

// --- Behavior subroutine calls (call keyword) ---

// resolveCallBehaviorName resolves a behavior name for a `call` statement.
// Handles direct lookup and namespace-qualified names (ns.bhv).
func (p *parser) resolveCallBehaviorName(tok token) (string, *bhvDef, error) {
	// Direct lookup
	if b := p.bhvs[tok.val]; b != nil {
		return tok.val, b, nil
	}
	// Namespace lookup
	ns := p.namespaceSets[tok.val]
	if ns != nil {
		peek, err := p.next()
		if err != nil {
			return "", nil, err
		}
		if peek.kind == tokDot {
			memberTok, err := p.expect(tokIdent)
			if err != nil {
				return "", nil, err
			}
			qualName := tok.val + "." + memberTok.val
			if ns.isPrivate(memberTok.val) {
				return "", nil, p.errorf(memberTok.pos, "cannot access private symbol %q in namespace %q", memberTok.val, tok.val)
			}
			if b, ok := ns.bhvs[memberTok.val]; ok {
				p.bhvs[qualName] = b
				return qualName, b, nil
			}
			return "", nil, p.errorf(memberTok.pos, "%q is not a behavior in namespace %q", memberTok.val, tok.val)
		}
		p.unget(peek)
	}
	return "", nil, p.errorf(tok.pos, "unknown behavior %q%s", tok.val, suggest(tok.val, collectKeys(p.bhvs)))
}

// parseCallBehaviorArgs parses keyword arguments for a `call` statement.
// Syntax: name: expr, name: out var, name: inout var
// Returns a map from parameter name to CallBhvArg.
func (p *parser) parseCallBehaviorArgs(bhv *bhvDef, resolve operandResolver, pos int) (map[string]*CallBhvArg, error) {
	args := map[string]*CallBhvArg{}

	// Build param lookup for validation
	paramByName := map[string]*paramDef{}
	for i := range bhv.params {
		paramByName[bhv.params[i].keyword] = &bhv.params[i]
	}

	// Check for parens
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	hasParen := peek.kind == tokLParen
	if !hasParen {
		p.unget(peek)
		// Check if there's anything to parse (next token is an identifier followed by colon)
		// Use save/restore since we need 2-token lookahead
		saved := p.save()
		peek, err = p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind != tokIdent {
			p.restore(saved)
			return args, nil // no args
		}
		peek2, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek2.kind != tokColon {
			p.restore(saved)
			return args, nil
		}
		// Restore and parse as keyword args
		p.restore(saved)
	}

	// Parse keyword arguments
	first := true
	for {
		if hasParen {
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokRParen {
				break
			}
			if !first {
				if peek.kind != tokComma {
					return nil, p.errorf(peek.pos, "expected ',' or ')' in call arguments, got %s", peek.describe())
				}
				peek, err = p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokRParen {
					break // trailing comma
				}
			}
			p.unget(peek)
		} else if !first {
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind != tokComma {
				p.unget(peek)
				break
			}
		}
		first = false

		// Parse: name: [direction] expr
		nameTok, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokColon); err != nil {
			return nil, p.errorf(nameTok.pos, "expected ':' after parameter name in call arguments")
		}

		// Validate parameter name
		param := paramByName[nameTok.val]
		if param == nil {
			return nil, p.errorf(nameTok.pos, "unknown parameter %q in call target", nameTok.val)
		}
		if args[nameTok.val] != nil {
			return nil, p.errorf(nameTok.pos, "duplicate argument %q in call", nameTok.val)
		}

		// Check for direction keyword (out, inout)
		dirTok, err := p.next()
		if err != nil {
			return nil, err
		}

		direction := ""
		if dirTok.kind == tokIdent && (dirTok.val == "out" || dirTok.val == "inout") {
			direction = dirTok.val
		} else {
			p.unget(dirTok)
		}

		// Validate direction matches parameter
		switch {
		case param.direction == "in" && direction != "":
			return nil, p.errorf(nameTok.pos, "parameter %q is 'in' but was passed as %q", nameTok.val, direction)
		case param.direction == "out" && direction != "out":
			return nil, p.errorf(nameTok.pos, "parameter %q is 'out' and must be passed with 'out' keyword", nameTok.val)
		case param.direction == "inout" && direction != "inout":
			return nil, p.errorf(nameTok.pos, "parameter %q is 'inout' and must be passed with 'inout' keyword", nameTok.val)
		}

		// Parse value expression
		var valExpr Expr
		if direction == "out" || direction == "inout" {
			// For out/inout, parse an lvalue directly (variable, $param, or $register)
			// Don't go through the full expression parser which does readability checks
			lvalTok, err := p.next()
			if err != nil {
				return nil, err
			}
			if lvalTok.kind != tokIdent {
				return nil, p.errorf(lvalTok.pos, "%s parameter %q requires a variable or register", direction, nameTok.val)
			}
			name := lvalTok.val
			if strings.HasPrefix(name, "$") {
				if reg, ok := unitRegisters[name]; ok {
					valExpr = &LiteralExpr{Value: reg}
				} else {
					// $param reference — keep as IdentExpr, resolved at emit time
					valExpr = &IdentExpr{Name: name}
				}
			} else {
				valExpr = &IdentExpr{Name: name}
			}
		} else {
			valExpr, err = p.parseArithExpr(resolve)
			if err != nil {
				return nil, err
			}
		}

		args[nameTok.val] = &CallBhvArg{
			Direction: direction,
			Value:     valExpr,
		}
	}

	return args, nil
}

// resolveCallSub resolves a behavior name to its "sub" value for the call instruction.
// Returns -1 for self-recursion, or a positive 1-based index into the dependencies array.
func (p *parser) resolveCallSub(behaviorID string, pos int) (int, error) {
	// Self-recursion
	if behaviorID == p.selfBehaviorID {
		return -1, nil
	}

	// Already compiled
	if idx, ok := p.depIndex[behaviorID]; ok {
		return idx, nil
	}

	// Cycle detection
	if p.depCompiling[behaviorID] {
		return 0, p.errorf(pos, "recursive call cycle involving %q", behaviorID)
	}

	// Compile on demand
	compiled, err := p.compileDependency(behaviorID, pos)
	if err != nil {
		return 0, err
	}

	// Add to dependencies array
	p.dependencies = append(p.dependencies, compiled)
	idx := len(p.dependencies) // 1-based
	p.depIndex[behaviorID] = idx
	return idx, nil
}

// compileDependency compiles a callee behavior on demand for use as a subroutine.
// Returns the compiled behavior value map (without dependencies — those go on root only).
func (p *parser) compileDependency(behaviorID string, pos int) (map[string]any, error) {
	bhv := p.bhvs[behaviorID]
	if bhv == nil {
		return nil, p.errorf(pos, "unknown behavior %q%s", behaviorID, suggest(behaviorID, collectKeys(p.bhvs)))
	}

	// Mark as compiling (cycle detection)
	p.depCompiling[behaviorID] = true
	defer delete(p.depCompiling, behaviorID)

	// Determine source offset
	sourceOffset := 0
	if bhv.prelude != "" {
		sourceOffset = len(bhv.prelude)
	}

	sourceDir := ""
	if bhv.sourcePath != "" {
		sourceDir = path.Dir(bhv.sourcePath)
		if sourceDir == "." {
			sourceDir = ""
		}
	}

	// Create a sub-parser for the callee's source
	dp := &parser{
		scanner: scanner{
			syn:          syntax.Scanner{Src: bhv.sourceText},
			locale:       p.locale,
			sourceFile:   bhv.sourcePath,
			sourceOffset: sourceOffset,
		},
		fns:         maps.Clone(p.fns),
		iters:       maps.Clone(p.iters),
		consts:      maps.Clone(p.consts),
		enums:       maps.Clone(p.enums),
		bhvs:        maps.Clone(p.bhvs),
		target:      behaviorID,
		loopLabels:  map[string]bool{},
		prelude:     bhv.prelude,
		sourceFS:    bhv.sourceFS,
		sourcePath:  bhv.sourcePath,
		sourceDir:   sourceDir,
		stdlibFS:    p.stdlibFS,
		releaseMode: p.releaseMode,
		// Share dependency tracking with root
		dependencies:   p.dependencies,
		depIndex:       p.depIndex,
		depCompiling:   p.depCompiling,
		selfBehaviorID: behaviorID,
	}

	// Skip pass 1 — the sub-parser already has all symbols cloned from the root.
	// Just find and compile the target behavior.
	dp.syn.Pos = 0
	dp.ungot = nil
	for {
		tok, err := dp.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokEOF {
			return nil, p.errorf(pos, "behavior %q not found in source", behaviorID)
		}
		if tok.kind != tokIdent {
			continue
		}
		switch tok.val {
		case "behavior":
			idTok, err := dp.parseBehaviorID()
			if err != nil {
				return nil, err
			}
			if idTok.val == behaviorID {
				obj, err := dp.parseBehaviorBody(behaviorID)
				if err != nil {
					return nil, err
				}
				// Sync shared state back
				p.dependencies = dp.dependencies
				p.warnings = append(p.warnings, dp.warnings...)

				value := obj.Value.(map[string]any)
				delete(value, "dependencies") // dependencies are flat on root
				return value, nil
			}
			if err := dp.skipBraceBlock(); err != nil {
				return nil, err
			}
		case "fn", "iter":
			if err := dp.skipFnDef(); err != nil {
				return nil, err
			}
		case "const", "import", "skip":
			if err := dp.skipToNextDecl(); err != nil {
				return nil, err
			}
		case "private":
			privTok, err := dp.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			switch privTok.val {
			case "fn", "iter":
				if err := dp.skipFnDef(); err != nil {
					return nil, err
				}
			case "const":
				if err := dp.skipToNextDecl(); err != nil {
					return nil, err
				}
			case "enum":
				if _, err := dp.expect(tokIdent); err != nil {
					return nil, err
				}
				if err := dp.skipBraceBlock(); err != nil {
					return nil, err
				}
			}
		case "enum":
			if _, err := dp.expect(tokIdent); err != nil {
				return nil, err
			}
			if err := dp.skipBraceBlock(); err != nil {
				return nil, err
			}
		}
	}
}

// emitBhvCallBehavior emits a call instruction frame at behavior level.
// resolveBhvCallArgValue resolves a call argument expression to a wire-format value
// at behavior level. Handles $param references (which resolve to parameter indices)
// and delegates other expressions to emitBhvExprGetValue.
func (p *parser) resolveBhvCallArgValue(expr Expr, syms *symbolTable, b *frameBuilder) (any, error) {
	if ident, ok := expr.(*IdentExpr); ok && strings.HasPrefix(ident.Name, "$") {
		// Check unit registers first
		if reg, ok := unitRegisters[ident.Name]; ok {
			return reg, nil
		}
		// Check param map
		if idx, ok := syms.paramMap[ident.Name]; ok {
			return idx, nil
		}
		return nil, fmt.Errorf("unknown register %q", ident.Name)
	}
	return p.emitBhvExprGetValue(expr, syms, b, "")
}

func (p *parser) emitBhvCallBehavior(stmt *CallBehaviorStmt, b *frameBuilder, syms *symbolTable) error {
	bhv := p.bhvs[stmt.BehaviorName]

	sub, err := p.resolveCallSub(stmt.BehaviorName, stmt.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	// Wire numbered slots to args (0-based, matching instruction slot numbering)
	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := stmt.Args[param.keyword]
		if !ok {
			continue // omitted param
		}

		val, err := p.resolveBhvCallArgValue(arg.Value, syms, b)
		if err != nil {
			return err
		}
		f[slotKey] = val
	}

	setComment(f, stmt.Comment)
	b.emit(f)

	// After a call, execution mode is unknown (callee may have changed it)
	b.mode = modeUnknown
	return nil
}

// emitBhvCallBehaviorExpr emits a call instruction for expression-form behavior calls.
// Unbound out params are captured as return values into the given target registers.
func (p *parser) emitBhvCallBehaviorExpr(expr *CallBehaviorExpr, retVals []any, syms *symbolTable, b *frameBuilder, comment string) error {
	bhv := p.bhvs[expr.BehaviorName]

	sub, err := p.resolveCallSub(expr.BehaviorName, expr.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	// Collect unbound out params for return value mapping
	retIdx := 0
	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := expr.Args[param.keyword]
		if ok {
			val, err := p.resolveBhvCallArgValue(arg.Value, syms, b)
			if err != nil {
				return err
			}
			f[slotKey] = val
		} else if param.direction == "out" || param.direction == "inout" {
			// Unbound out/inout param — map to return value
			if retIdx < len(retVals) {
				f[slotKey] = retVals[retIdx]
				retIdx++
			}
			// else: unbound with no target — omit (discarded)
		}
		// else: unbound in param — omit (null)
	}

	setComment(f, comment)
	b.emit(f)

	b.mode = modeUnknown
	return nil
}

// emitFnBodyCallBehavior emits a call instruction frame within a function body context.
func (p *parser) emitFnBodyCallBehavior(stmt *CallBehaviorStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string) error {
	bhv := p.bhvs[stmt.BehaviorName]

	sub, err := p.resolveCallSub(stmt.BehaviorName, stmt.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := stmt.Args[param.keyword]
		if !ok {
			continue
		}

		val, err := p.emitExprGetValue(arg.Value, b, paramMap, usedVars, "", stmt.Pos)
		if err != nil {
			return err
		}
		f[slotKey] = val
	}

	setComment(f, comment)
	b.emit(f)
	b.mode = modeUnknown
	return nil
}

// emitFnBodyCallBehaviorExpr emits a call instruction for expression-form behavior calls in fn body.
func (p *parser) emitFnBodyCallBehaviorExpr(expr *CallBehaviorExpr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string) error {
	bhv := p.bhvs[expr.BehaviorName]

	sub, err := p.resolveCallSub(expr.BehaviorName, expr.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	retIdx := 0
	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := expr.Args[param.keyword]
		if ok {
			val, err := p.emitExprGetValue(arg.Value, b, paramMap, usedVars, "", expr.Pos)
			if err != nil {
				return err
			}
			f[slotKey] = val
		} else if param.direction == "out" || param.direction == "inout" {
			if retIdx < len(retVals) {
				f[slotKey] = retVals[retIdx]
				retIdx++
			}
		}
	}

	setComment(f, comment)
	b.emit(f)
	b.mode = modeUnknown
	return nil
}
