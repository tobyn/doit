package compiler

import (
	"fmt"
	"io/fs"
	"maps"
	"reflect"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
)

// --- Stdlib ---

func parseStdlib(stdlib fs.FS) (map[string]*fnDef, error) {
	matches, err := fs.Glob(stdlib, "*.doit")
	if err != nil {
		return nil, err
	}

	fns := map[string]*fnDef{}
	for _, path := range matches {
		data, err := fs.ReadFile(stdlib, path)
		if err != nil {
			return nil, err
		}
		if err := parseStdlibFile(string(data), fns); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return fns, nil
}

func isDirection(val string) bool {
	return val == "in" || val == "out" || val == "inout"
}

// --- AST-based fn body parsing ---

// checkFnBodyExprDeclared checks that all IdentExpr nodes in an expression
// produced by parseFnBodyExpr are declared (as parameters or local variables).
func (p *parser) checkFnBodyExprDeclared(expr Expr, ctx *fnBodyContext, pos int) error {
	switch e := expr.(type) {
	case *IdentExpr:
		if _, ok := ctx.paramDirs[e.Name]; ok {
			return nil
		}
		if _, ok := ctx.fnVarInfo[e.Name]; ok {
			return nil
		}
		if _, ok := p.consts[e.Name]; ok {
			return nil
		}
		if e.Name == "Unit" {
			return p.errorf(pos, "Unit has no constructor; unit values are produced by instructions at runtime")
		}
		return p.errorf(pos, "unknown function or variable %q", e.Name)
	case *ArithExpr:
		if err := p.checkFnBodyExprDeclared(e.LHS, ctx, pos); err != nil {
			return err
		}
		return p.checkFnBodyExprDeclared(e.RHS, ctx, pos)
	case *AmpersandExpr:
		if err := p.checkFnBodyExprDeclared(e.Value, ctx, pos); err != nil {
			return err
		}
		return p.checkFnBodyExprDeclared(e.Num, ctx, pos)
	case *ConstructorExpr:
		for _, arg := range e.Args {
			if err := p.checkFnBodyExprDeclared(arg, ctx, pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// fnBodyResolver returns an operandResolver for fn body contexts.
// It resolves $registers to literals, checks out-only params are not read,
// marks fn body variables as used, and returns IdentExpr for all other identifiers.
func (p *parser) fnBodyResolver(ctx *fnBodyContext) operandResolver {
	return func(tok token) (Expr, error) {
		if strings.HasPrefix(tok.val, "$") {
			if reg, ok := unitRegisters[tok.val]; ok {
				return &LiteralExpr{Value: reg}, nil
			}
			return nil, p.errorf(tok.pos, "unknown unit register %q", tok.val)
		}
		if dir, ok := ctx.paramDirs[tok.val]; ok {
			if dir == "out" {
				return nil, p.errorf(tok.pos, "cannot read from output parameter %q", tok.val)
			}
		} else if _, ok := ctx.fnVarInfo[tok.val]; !ok {
			// Check constants before erroring
			if c, ok := p.consts[tok.val]; ok {
				return &LiteralExpr{Value: c.value}, nil
			}
			if tok.val == "Unit" {
				return nil, p.errorf(tok.pos, "Unit has no constructor; unit values are produced by instructions at runtime")
			}
			return nil, p.errorf(tok.pos, "unknown function or variable %q", tok.val)
		}
		ctx.markFnVarUsed(tok.val)
		return &IdentExpr{Name: tok.val}, nil
	}
}

// parseFnBodyExpr parses a single expression in a fn body context.
// Accepts strings, identifiers, numbers, null, $register references,
// type constructors, the & operator, and localize blocks.
func (p *parser) parseFnBodyExpr() (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	var base Expr
	switch tok.kind {
	case tokString:
		base = &LiteralExpr{Value: tok.val}
	case tokMinus:
		// Unary minus: -<number> or -<variable>
		innerTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if innerTok.kind == tokNumber {
			num, _ := strconv.Atoi(innerTok.val)
			base = &LiteralExpr{Value: map[string]any{"num": -num}}
		} else if innerTok.kind == tokIdent && !isConstructor(innerTok.val) && innerTok.val != "null" && innerTok.val != "false" && innerTok.val != "true" {
			base = &ArithExpr{
				Op:  tokMinus,
				LHS: &LiteralExpr{Value: map[string]any{"num": 0}},
				RHS: &IdentExpr{Name: innerTok.val},
			}
		} else {
			return nil, p.errorf(tok.pos, "expected number or variable after '-'")
		}
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		base = &LiteralExpr{Value: map[string]any{"num": num}}
	case tokIdent:
		if tok.val == "localize" {
			resolved, err := p.parseLocalize()
			if err != nil {
				return nil, err
			}
			base = &LiteralExpr{Value: resolved}
		} else if tok.val == "null" || tok.val == "false" {
			base = &LiteralExpr{Value: false}
		} else if tok.val == "true" {
			base = &LiteralExpr{Value: map[string]any{"num": 1}}
		} else if isConstructor(tok.val) {
			ctor, err := p.parseFnBodyConstructorExpr(tok)
			if err != nil {
				return nil, err
			}
			base = ctor
		} else if strings.HasPrefix(tok.val, "$") {
			if reg, ok := unitRegisters[tok.val]; ok {
				base = &LiteralExpr{Value: reg}
			} else {
				return nil, p.errorf(tok.pos, "unknown unit register %q", tok.val)
			}
		} else if c, ok := p.consts[tok.val]; ok {
			base = &LiteralExpr{Value: c.value}
		} else {
			base = &IdentExpr{Name: tok.val}
		}
	default:
		return nil, p.errorf(tok.pos, "expected argument value, got %s", tok.describe())
	}

	// Check for & operator
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind == tokAmpersand {
		if ctor, ok := base.(*ConstructorExpr); ok && ctor.TypeName == "Range" {
			return nil, p.errorf(peek.pos, "'&' cannot be used with Range (it would overwrite the step field)")
		}
		numExpr, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		return &AmpersandExpr{Value: base, Num: numExpr}, nil
	}
	p.unget(peek)
	return base, nil
}

// parseFnBodyArgExpr parses a single argument expression in a fn body call.
// Handles mode block expressions and if-expressions in addition to the
// standard parseFnBodyExpr types.
func (p *parser) parseFnBodyArgExpr(ctx *fnBodyContext) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokIdent && (tok.val == "locked" || tok.val == "unlocked") {
		mbe, err := p.parseFnBodyModeBlockExpr(tok.val == "unlocked", ctx, "")
		if err != nil {
			return nil, err
		}
		return p.parseArithExprFromFull(Expr(mbe), ctx.resolve)
	}
	if tok.kind == tokIdent && tok.val == "if" {
		ifExpr, err := p.parseFnBodyIfExpr(ctx, "")
		if err != nil {
			return nil, err
		}
		return p.parseArithExprFromFull(Expr(ifExpr), ctx.resolve)
	}
	if tok.kind == tokIdent && p.callExprParser != nil {
		name, callee, fnErr := p.resolveFnName(tok)
		if fnErr != nil {
			return nil, fnErr
		}
		if callee != nil && callee.hasReturn() {
			callExpr, err := p.callExprParser(callee, token{kind: tokIdent, val: name, pos: tok.pos})
			if err != nil {
				return nil, err
			}
			return p.parseArithExprFromFull(callExpr, ctx.resolve)
		}
	}
	if tok.kind == tokLParen {
		// Parenthesized expression: (a > 5), (a + 1), etc.
		inner, err := p.parseBoolExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		if truthy, ok := inner.(*TruthyExpr); ok {
			return p.parseArithExprFromFull(truthy.Value, ctx.resolve)
		}
		return inner, nil
	}
	p.unget(tok)
	expr, err := p.parseFnBodyExpr()
	if err != nil {
		return nil, err
	}
	if err := p.checkFnBodyExprDeclared(expr, ctx, tok.pos); err != nil {
		return nil, err
	}
	ctx.markExprUsed(expr)
	return expr, nil
}

// parseFnBodyConstructorExpr parses a type constructor in a fn body,
// returning a ConstructorExpr AST node.
func (p *parser) parseFnBodyConstructorExpr(nameTok token) (Expr, error) {
	return p.parseConstructorExpr(nameTok, p.parseFnBodyExpr)
}

// parseFnBodyCallArgs parses positional and keyword arguments for a
// function call in a fn body, returning AST-typed expressions.
// Supports both unparenthesized and parenthesized call syntax.
func (p *parser) parseFnBodyCallArgs(callee *fnDef, calleeTok token, ctx *fnBodyContext) ([]Expr, map[string]Expr, error) {
	paramDirs := ctx.paramDirs
	letVars := ctx.fnVarInfo
	// Detect parenthesized call syntax
	paren := false
	peek, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if peek.kind == tokLParen {
		paren = true
	} else {
		p.unget(peek)
	}

	posCount := callee.positionalCount()
	args := make([]Expr, posCount)
	for i := 0; i < posCount; i++ {
		if paren && i > 0 {
			if _, err := p.expect(tokComma); err != nil {
				return nil, nil, err
			}
		}

		// Peek for direction annotation
		dirTok, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		annotation := ""
		annotationPos := dirTok.pos
		if dirTok.kind == tokIdent && isDirection(dirTok.val) {
			annotation = dirTok.val
		} else {
			p.unget(dirTok)
			annotationPos = dirTok.pos
		}

		pd := callee.positionalParam(i)
		if err := p.checkCallAnnotation(annotation, pd, calleeTok.val, annotationPos); err != nil {
			return nil, nil, err
		}

		arg, err := p.parseFnBodyArgExpr(ctx)
		if err != nil {
			return nil, nil, err
		}
		// In parenthesized call mode, try boolean continuation
		// to support: my_fn(a > 5), my_fn(a, b == c)
		if paren {
			cont, handled, err := p.maybeExprContinuation(arg, ctx.resolve)
			if err != nil {
				return nil, nil, err
			}
			if handled {
				arg = cont
			}
		}
		args[i] = arg
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err = p.next()
	if err != nil {
		return nil, nil, err
	}
	if (peek.kind == tokString || peek.kind == tokIdent) && callee.positionalCount() < len(callee.params) {
		if peek.kind == tokString {
			return nil, nil, p.errorf(peek.pos,
				"too many positional arguments for %s (remaining parameters are keyword-only)", calleeTok.val)
		}
		p.unget(peek)
	} else if peek.kind == tokComma && callee.positionalCount() < len(callee.params) {
		kwArgs = map[string]Expr{}
		for {
			dirOrKw, err := p.expect(tokIdent)
			if err != nil {
				return nil, nil, err
			}
			annotation := ""
			annotationPos := dirOrKw.pos
			if isDirection(dirOrKw.val) {
				annotation = dirOrKw.val
				dirOrKw, err = p.expect(tokIdent)
				if err != nil {
					return nil, nil, err
				}
			}
			kw := callee.keywordByName(dirOrKw.val)
			if kw == nil {
				return nil, nil, p.errorf(dirOrKw.pos, "unknown keyword argument %q", dirOrKw.val)
			}
			if _, exists := kwArgs[dirOrKw.val]; exists {
				return nil, nil, p.errorf(dirOrKw.pos, "duplicate keyword argument %q", dirOrKw.val)
			}
			if err := p.checkCallAnnotation(annotation, kw, calleeTok.val, annotationPos); err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, nil, err
			}
			val, err := p.parseFnBodyArgExpr(ctx)
			if err != nil {
				return nil, nil, err
			}
			// In parenthesized call mode, try boolean continuation
			if paren {
				cont, handled, err := p.maybeExprContinuation(val, ctx.resolve)
				if err != nil {
					return nil, nil, err
				}
				if handled {
					val = cont
				}
			}
			kwArgs[dirOrKw.val] = val

			next, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			if next.kind != tokComma {
				p.unget(next)
				break
			}
		}
	} else {
		p.unget(peek)
	}

	if paren {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, nil, err
		}
	}

	if err := p.checkFnBodyCallDirectionsExpr(callee, calleeTok.val, args, kwArgs, paramDirs, letVars, calleeTok.pos); err != nil {
		return nil, nil, err
	}

	return args, kwArgs, nil
}

// fnBodyExprDir determines the effective direction of an AST expression
// in a fn body context.
func fnBodyExprDir(expr Expr, paramDirs map[string]string, fnVars map[string]fnVarInfo) string {
	if e, ok := expr.(*IdentExpr); ok {
		if dir, ok := paramDirs[e.Name]; ok {
			return dir
		}
		if info, declared := fnVars[e.Name]; declared {
			if info.mutable {
				return "inout"
			}
			return "in"
		}
		return "inout"
	}
	return "in" // literals, constructors, etc.
}

// checkFnBodyCallDirectionsExpr checks direction compatibility for AST-typed args.
func (p *parser) checkFnBodyCallDirectionsExpr(callee *fnDef, calleeName string, args []Expr, kwArgs map[string]Expr, paramDirs map[string]string, letVars map[string]fnVarInfo, pos int) error {
	posIdx := 0
	for _, pd := range callee.params {
		calleeDir := pd.effectiveDirection()
		if pd.keyword == "" {
			if posIdx < len(args) {
				aDir := fnBodyExprDir(args[posIdx], paramDirs, letVars)
				if !canPass(calleeDir, aDir) {
					return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
						aDir, calleeDir, pd.name, calleeName)
				}
			}
			posIdx++
		} else if kwArgs != nil {
			if val, ok := kwArgs[pd.keyword]; ok {
				aDir := fnBodyExprDir(val, paramDirs, letVars)
				if !canPass(calleeDir, aDir) {
					return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
						aDir, calleeDir, pd.name, calleeName)
				}
			}
		}
	}
	return nil
}

// --- AST-based fn body emission ---

// collectASTOutputVars pre-scans AST statements for output variables
// and adds them to paramMap with unique names (for inline renaming).
func collectASTOutputVars(stmts []Stmt, paramMap map[string]any, usedVars map[string]bool) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *LetStmt:
			if _, mapped := paramMap[s.Name]; !mapped {
				paramMap[s.Name] = allocUniqueVar(s.Name, usedVars)
			}
			collectExprOutputVars(s.Value, paramMap, usedVars)
		case *AssignStmt:
			if _, mapped := paramMap[s.Target]; !mapped {
				paramMap[s.Target] = allocUniqueVar(s.Target, usedVars)
			}
			collectExprOutputVars(s.Value, paramMap, usedVars)
		case *CompoundAssignStmt:
			if _, mapped := paramMap[s.Target]; !mapped {
				paramMap[s.Target] = allocUniqueVar(s.Target, usedVars)
			}
		case *IncrDecrStmt:
			if _, mapped := paramMap[s.Target]; !mapped {
				paramMap[s.Target] = allocUniqueVar(s.Target, usedVars)
			}
		case *MultiReturnStmt:
			for _, bind := range s.Bindings {
				if !bind.Discard {
					if _, mapped := paramMap[bind.Name]; !mapped {
						paramMap[bind.Name] = allocUniqueVar(bind.Name, usedVars)
					}
				}
			}
			collectExprOutputVars(s.Value, paramMap, usedVars)
		case *IfStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
			for _, elif := range s.ElseIfs {
				collectASTOutputVars(elif.Body, paramMap, usedVars)
			}
			collectASTOutputVars(s.Else, paramMap, usedVars)
		case *WhileStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *LoopStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *ForStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *ModeBlockStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *WaitStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		}
	}
}

// collectExprOutputVars recursively scans an expression for nested statement
// bodies (IfExpr, ModeBlockExpr) that may declare output variables.
func collectExprOutputVars(expr Expr, paramMap map[string]any, usedVars map[string]bool) {
	switch e := expr.(type) {
	case *IfExpr:
		collectASTOutputVars(e.Body, paramMap, usedVars)
		collectExprOutputVars(e.Tail, paramMap, usedVars)
		for _, elif := range e.ElseIfs {
			collectASTOutputVars(elif.Body, paramMap, usedVars)
			collectExprOutputVars(elif.Tail, paramMap, usedVars)
		}
		collectASTOutputVars(e.ElsBody, paramMap, usedVars)
		collectExprOutputVars(e.ElsTail, paramMap, usedVars)
	case *ModeBlockExpr:
		collectASTOutputVars(e.Body, paramMap, usedVars)
		collectExprOutputVars(e.Tail, paramMap, usedVars)
	case *ExprListExpr:
		for _, item := range e.Exprs {
			collectExprOutputVars(item, paramMap, usedVars)
		}
	}
}

// resolveVarName resolves a variable name through paramMap.
func resolveVarName(name string, paramMap map[string]any) any {
	if val, ok := paramMap[name]; ok {
		return val
	}
	return name
}

// emitCallExprArgs resolves AST call args through paramMap, emitting
// complex expressions (constructors, &) to temp variables as needed.
func (p *parser) emitCallExprArgs(args []Expr, kwArgs map[string]Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, pos int) ([]any, map[string]any, error) {
	resolvedArgs := make([]any, len(args))
	for i, arg := range args {
		val, err := p.emitExprGetValue(arg, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, nil, err
		}
		resolvedArgs[i] = val
	}
	resolvedKwArgs := map[string]any{}
	for kw, arg := range kwArgs {
		val, err := p.emitExprGetValue(arg, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, nil, err
		}
		resolvedKwArgs[kw] = val
	}
	return resolvedArgs, resolvedKwArgs, nil
}

// tryResolveConstructorLiteral attempts to resolve a ConstructorExpr to a
// compile-time literal. Returns (nil, false) if any argument is not a literal.
func tryResolveConstructorLiteral(ctor *ConstructorExpr) (any, bool) {
	prefix := ""
	switch ctor.TypeName {
	case "Component":
		prefix = "c_"
	case "Technology":
		prefix = "t_"
	case "Value":
		prefix = "v_"
	}
	switch ctor.TypeName {
	case "Item", "Component", "Technology", "Value":
		lit, ok := ctor.Args[0].(*LiteralExpr)
		if !ok {
			return nil, false
		}
		s, ok := lit.Value.(string)
		if !ok {
			return nil, false
		}
		return map[string]any{"id": prefix + s}, true
	case "Coordinate":
		xLit, xOk := ctor.Args[0].(*LiteralExpr)
		yLit, yOk := ctor.Args[1].(*LiteralExpr)
		if !xOk || !yOk {
			return nil, false
		}
		xMap, xIsMap := xLit.Value.(map[string]any)
		yMap, yIsMap := yLit.Value.(map[string]any)
		if !xIsMap || !yIsMap {
			return nil, false
		}
		xNum, xHas := xMap["num"]
		yNum, yHas := yMap["num"]
		if !xHas || !yHas || len(xMap) != 1 || len(yMap) != 1 {
			return nil, false
		}
		return map[string]any{"coord": map[string]any{"x": xNum, "y": yNum}}, true
	case "Range":
		// Range has 3 args: start, stop, step (all normalized during parsing)
		startLit, sOk := ctor.Args[0].(*LiteralExpr)
		stopLit, eOk := ctor.Args[1].(*LiteralExpr)
		stepLit, tOk := ctor.Args[2].(*LiteralExpr)
		if !sOk || !eOk || !tOk {
			return nil, false
		}
		startMap, sIsMap := startLit.Value.(map[string]any)
		stopMap, eIsMap := stopLit.Value.(map[string]any)
		stepMap, tIsMap := stepLit.Value.(map[string]any)
		if !sIsMap || !eIsMap || !tIsMap {
			return nil, false
		}
		startNum, sHas := startMap["num"]
		stopNum, eHas := stopMap["num"]
		stepNum, tHas := stepMap["num"]
		if !sHas || !eHas || !tHas || len(startMap) != 1 || len(stopMap) != 1 || len(stepMap) != 1 {
			return nil, false
		}
		return map[string]any{
			"coord": map[string]any{"x": startNum, "y": stopNum},
			"num":   stepNum,
		}, true
	}
	return nil, false
}

// tryResolveAmpersandLiteral attempts to resolve an AmpersandExpr to a
// compile-time literal. Returns (nil, false) if any operand is not compile-time.
func tryResolveAmpersandLiteral(amp *AmpersandExpr) (any, bool) {
	// Resolve LHS
	var baseVal map[string]any
	switch lhs := amp.Value.(type) {
	case *LiteralExpr:
		if m, ok := lhs.Value.(map[string]any); ok {
			baseVal = m
		}
	case *ConstructorExpr:
		if v, ok := tryResolveConstructorLiteral(lhs); ok {
			if m, ok := v.(map[string]any); ok {
				baseVal = m
			}
		}
	}
	if baseVal == nil {
		return nil, false
	}
	// Resolve RHS
	numLit, ok := amp.Num.(*LiteralExpr)
	if !ok {
		return nil, false
	}
	numMap, ok := numLit.Value.(map[string]any)
	if !ok {
		return nil, false
	}
	numN, ok := numMap["num"]
	if !ok || len(numMap) != 1 {
		return nil, false
	}
	result := make(map[string]any, len(baseVal)+1)
	for k, v := range baseVal {
		result[k] = v
	}
	result["num"] = numN
	return result, true
}

// emitExprGetValue emits an expression and returns the resolved value.
// For simple literals/idents, returns without emitting. For compile-time
// constructors/ampersands, returns the literal. For runtime expressions,
// emits to a temp variable and returns the temp name.
func (p *parser) emitExprGetValue(expr Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) (any, error) {
	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Value, nil
	case *IdentExpr:
		return resolveVarName(e.Name, paramMap), nil
	case *ConstructorExpr:
		if val, ok := tryResolveConstructorLiteral(e); ok {
			return val, nil
		}
		tempName := allocUniqueVar("@ctor", usedVars)
		if err := p.emitConstructorTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	case *AmpersandExpr:
		if val, ok := tryResolveAmpersandLiteral(e); ok {
			return val, nil
		}
		tempName := allocUniqueVar("@ctor", usedVars)
		if err := p.emitAmpersandTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	default:
		tempName := allocUniqueVar("@ctor", usedVars)
		if err := p.emitExprTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	}
}

// emitExprTo emits an expression writing the result to target.
func (p *parser) emitExprTo(expr Expr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	switch e := expr.(type) {
	case *CallExpr:
		resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(e.Args, e.KwArgs, b, paramMap, usedVars, pos)
		if err != nil {
			return err
		}
		return p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{target}, b, pos, comment, usedVars)
	case *InstructionExpr:
		resolved := resolveInstructionFrame(e.Frame, []any{target}, paramMap, nil, comment)
		b.emit(resolved)
		return nil
	case *ConstructorExpr:
		return p.emitConstructorTo(e, target, b, paramMap, usedVars, comment, pos)
	case *AmpersandExpr:
		return p.emitAmpersandTo(e, target, b, paramMap, usedVars, comment, pos)
	case *LiteralExpr:
		f := map[string]any{"op": "set_reg", "1": e.Value, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	case *IdentExpr:
		val := resolveVarName(e.Name, paramMap)
		f := map[string]any{"op": "set_reg", "1": val, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	case *ArithExpr:
		return p.emitFnArithTo(e, target, b, paramMap, usedVars, comment, pos)
	case *CompareExpr, *TypeCheckExpr, *TruthyExpr, *BoolChainExpr, *NotExpr:
		return p.emitFnBoolExprTo(expr, target, b, paramMap, usedVars, comment, pos)
	case *ModeBlockExpr:
		return p.emitFnModeBlockExpr(e, target, b, paramMap, usedVars, comment, pos)
	case *IfExpr:
		return p.emitFnIfExpr(e, target, b, paramMap, usedVars, comment, pos)
	}
	return fmt.Errorf("unsupported expression type %T in emitExprTo", expr)
}

// emitFnModeBlockExpr emits a mode block expression in a fn body context.
func (p *parser) emitFnModeBlockExpr(e *ModeBlockExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	mbeComment := e.Comment
	if mbeComment == "" {
		mbeComment = comment
	}
	saved := emitModeEntry(b, e.Unlock, mbeComment)
	if err := p.emitFnBody(e.Body, b, paramMap, usedVars, comment, pos); err != nil {
		return err
	}
	if err := p.emitExprTo(e.Tail, target, b, paramMap, usedVars, mbeComment, pos); err != nil {
		return err
	}
	emitModeExit(b, saved)
	return nil
}

// emitFnModeBlockExprMulti emits a mode block expression with a multi-return
// tail, directing return values to the given retVals slice.
func (p *parser) emitFnModeBlockExprMulti(e *ModeBlockExpr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	mbeComment := e.Comment
	if mbeComment == "" {
		mbeComment = comment
	}
	saved := emitModeEntry(b, e.Unlock, mbeComment)
	if err := p.emitFnBody(e.Body, b, paramMap, usedVars, comment, pos); err != nil {
		return err
	}
	ce, ok := e.Tail.(*CallExpr)
	if !ok {
		return fmt.Errorf("multi-return mode block expression tail must be a call, got %T", e.Tail)
	}
	resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(ce.Args, ce.KwArgs, b, paramMap, usedVars, pos)
	if err != nil {
		return err
	}
	if err := p.expandCall(ce.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, mbeComment, usedVars); err != nil {
		return err
	}
	emitModeExit(b, saved)
	return nil
}

// emitConstructorTo emits a type constructor writing the result to target.
func (p *parser) emitConstructorTo(ctor *ConstructorExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	// Try compile-time resolution first (based on AST types, not resolved values)
	if val, ok := tryResolveConstructorLiteral(ctor); ok {
		f := map[string]any{"op": "set_reg", "1": val, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	// Runtime path — Coordinate and Range can be runtime
	if ctor.TypeName == "Coordinate" {
		xVal, err := p.emitExprGetValue(ctor.Args[0], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		yVal, err := p.emitExprGetValue(ctor.Args[1], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		return p.expandCall("combine_coordinate", []any{xVal, yVal}, nil, []any{target}, b, pos, comment, usedVars)
	}
	if ctor.TypeName == "Range" {
		// combine_register step, false, x: start, y: stop
		stepVal, err := p.emitExprGetValue(ctor.Args[2], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		startVal, err := p.emitExprGetValue(ctor.Args[0], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		stopVal, err := p.emitExprGetValue(ctor.Args[1], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		kwArgs := map[string]any{"x": startVal, "y": stopVal}
		return p.expandCall("combine_register", []any{stepVal, false}, kwArgs, []any{target}, b, pos, comment, usedVars)
	}
	return fmt.Errorf("unknown constructor %q", ctor.TypeName)
}

// emitAmpersandTo emits an & expression writing the result to target.
func (p *parser) emitAmpersandTo(amp *AmpersandExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	// Try compile-time resolution (based on AST types)
	if val, ok := tryResolveAmpersandLiteral(amp); ok {
		f := map[string]any{"op": "set_reg", "1": val, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	// Runtime: emit set_number
	baseVal, err := p.emitExprGetValue(amp.Value, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	numVal, err := p.emitExprGetValue(amp.Num, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	return p.expandCall("set_number", []any{baseVal, numVal}, nil, []any{target}, b, pos, comment, usedVars)
}

// emitFnArithTo emits an arithmetic expression writing the result to target.
func (p *parser) emitFnArithTo(expr *ArithExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	ac := &arithCounter{}
	_, err := p.emitArithNode(expr, target, b, usedVars, comment, ac, func(e Expr) (any, error) {
		return p.emitExprGetValue(e, b, paramMap, usedVars, "", pos)
	})
	return err
}

// emitFnBoolExprTo emits a boolean expression (comparison/typecheck/truthy/chain)
// writing the result to target. Mirrors emitBhvBoolExprTo but resolves operands
// through paramMap.
func (p *parser) emitFnBoolExprTo(expr Expr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	resolved, err := p.resolveFnBoolTree(expr, b, paramMap, usedVars, pos)
	if err != nil {
		return err
	}
	p.emitResolvedBoolExprTo(resolved, target, b, comment)
	return nil
}

// resolveFnBoolTree walks an Expr tree, resolving operands through paramMap
// and emitting arithmetic frames. Produces a resolvedBoolExpr tree.
// resolveFnBoolTree delegates to the unified resolveBoolTree with
// fn body operand resolution.
func (p *parser) resolveFnBoolTree(expr Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, pos int) (*resolvedBoolExpr, error) {
	return p.resolveBoolTree(expr, func(e Expr) (any, error) {
		return p.emitExprGetValue(e, b, paramMap, usedVars, "", pos)
	})
}

// emitFnBody emits frames for an AST body during call expansion.
func (p *parser) emitFnBody(stmts []Stmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	collectASTOutputVars(stmts, paramMap, usedVars)

	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *InstructionStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			resolved := resolveInstructionFrame(s.Frame, nil, paramMap, nil, callComment)
			b.emit(resolved)

		case *ModeBlockStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			saved := emitModeEntry(b, s.Unlock, callComment)
			if err := p.emitFnBody(s.Body, b, paramMap, usedVars, comment, pos); err != nil {
				return err
			}
			emitModeExit(b, saved)

		case *CallStmt:
			resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(s.Args, s.KwArgs, b, paramMap, usedVars, pos)
			if err != nil {
				return err
			}
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.expandCall(s.Name, resolvedArgs, resolvedKwArgs, nil, b, pos, callComment, usedVars); err != nil {
				return err
			}

		case *LetStmt:
			target := resolveVarName(s.Name, paramMap)
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitExprTo(s.Value, target, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *MultiReturnStmt:
			retVals := make([]any, len(s.Bindings))
			for i, bind := range s.Bindings {
				if bind.Discard {
					retVals[i] = false
				} else {
					retVals[i] = resolveVarName(bind.Name, paramMap)
				}
			}
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			switch v := s.Value.(type) {
			case *CallExpr:
				resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(v.Args, v.KwArgs, b, paramMap, usedVars, pos)
				if err != nil {
					return err
				}
				if err := p.expandCall(v.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, callComment, usedVars); err != nil {
					return err
				}
			case *ModeBlockExpr:
				if err := p.emitFnModeBlockExprMulti(v, retVals, b, paramMap, usedVars, callComment, pos); err != nil {
					return err
				}
			case *IfExpr:
				if err := p.emitFnIfExprMulti(v, retVals, b, paramMap, usedVars, callComment, pos); err != nil {
					return err
				}
			case *InstructionExpr:
				resolved := resolveInstructionFrame(v.Frame, retVals, paramMap, nil, callComment)
				b.emit(resolved)
			case *ExprListExpr:
				bindIdx := 0
				for _, expr := range v.Exprs {
					switch e := expr.(type) {
					case *CallExpr:
						fn := p.fns[e.Name]
						arity := fn.returnCount()
						remaining := len(s.Bindings) - bindIdx
						callArity := arity
						if callArity > remaining {
							callArity = remaining
						}
						callRetVals := retVals[bindIdx : bindIdx+callArity]
						resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(e.Args, e.KwArgs, b, paramMap, usedVars, pos)
						if err != nil {
							return err
						}
						if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, callRetVals, b, pos, callComment, usedVars); err != nil {
							return err
						}
						bindIdx += callArity
					case *ModeBlockExpr:
						arity := p.exprArity(e.Tail)
						remaining := len(s.Bindings) - bindIdx
						mbeArity := arity
						if mbeArity > remaining {
							mbeArity = remaining
						}
						if mbeArity == 1 {
							if !s.Bindings[bindIdx].Discard {
								if err := p.emitFnModeBlockExpr(e, retVals[bindIdx], b, paramMap, usedVars, callComment, pos); err != nil {
									return err
								}
							}
						} else {
							mbeRetVals := retVals[bindIdx : bindIdx+mbeArity]
							if err := p.emitFnModeBlockExprMulti(e, mbeRetVals, b, paramMap, usedVars, callComment, pos); err != nil {
								return err
							}
						}
						bindIdx += mbeArity
					case *IfExpr:
						arity := p.ifExprArity(e)
						remaining := len(s.Bindings) - bindIdx
						ifArity := arity
						if ifArity > remaining {
							ifArity = remaining
						}
						if ifArity == 1 {
							if !s.Bindings[bindIdx].Discard {
								if err := p.emitFnIfExpr(e, retVals[bindIdx], b, paramMap, usedVars, callComment, pos); err != nil {
									return err
								}
							}
						} else {
							ifRetVals := retVals[bindIdx : bindIdx+ifArity]
							if err := p.emitFnIfExprMulti(e, ifRetVals, b, paramMap, usedVars, callComment, pos); err != nil {
								return err
							}
						}
						bindIdx += ifArity
					default:
						if !s.Bindings[bindIdx].Discard {
							if err := p.emitExprTo(expr, retVals[bindIdx], b, paramMap, usedVars, callComment, pos); err != nil {
								return err
							}
						}
						bindIdx++
					}
				}
			}

		case *AssignStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitExprTo(s.Value, target, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *CompoundAssignStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			rhs, err := p.emitExprGetValue(s.Value, b, paramMap, usedVars, "", pos)
			if err != nil {
				return err
			}
			f := map[string]any{
				"op": compoundAssignOpName(s.Op),
				"1":  target,
				"2":  rhs,
				"3":  target,
			}
			setComment(f, callComment)
			b.emit(f)

		case *IncrDecrStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			op := "add"
			if s.Op == tokMinusMinus {
				op = "sub"
			}
			f := map[string]any{
				"op": op,
				"1":  target,
				"2":  map[string]any{"num": 1},
				"3":  target,
			}
			setComment(f, callComment)
			b.emit(f)

		case *IfStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitFnIfStmt(s, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *WhileStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitFnWhileStmt(s, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *LoopStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitFnLoopStmt(s, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *ForStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitFnForStmt(s, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *WaitStmt:
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			if err := p.emitFnWaitStmt(s, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *BreakStmt:
			// Emit placeholder frame that emitFnLoopStmt/emitFnWhileStmt will patch
			f := map[string]any{"op": "@break"}
			if s.Label != "" {
				f["label"] = s.Label
			}
			b.emit(f)

		case *ReturnStmt:
			// Emit values to @retK targets, then emit @return jump placeholder
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			retOffset := 0
			for _, val := range s.Values {
				switch e := val.(type) {
				case *CallExpr:
					callee := p.fns[e.Name]
					rc := callee.returnCount()
					retVals := make([]any, rc)
					for j := 0; j < rc; j++ {
						retVals[j] = resolveVarName("@ret"+strconv.Itoa(retOffset+j+1), paramMap)
					}
					resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(e.Args, e.KwArgs, b, paramMap, usedVars, pos)
					if err != nil {
						return err
					}
					if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, callComment, usedVars); err != nil {
						return err
					}
					retOffset += rc
				case *InstructionExpr:
					rc := frameReturnCount(e.Frame)
					retVals := make([]any, rc)
					for j := 0; j < rc; j++ {
						retVals[j] = resolveVarName("@ret"+strconv.Itoa(retOffset+j+1), paramMap)
					}
					resolved := resolveInstructionFrame(e.Frame, retVals, paramMap, nil, callComment)
					b.emit(resolved)
					retOffset += rc
				default:
					target := resolveVarName("@ret"+strconv.Itoa(retOffset+1), paramMap)
					if err := p.emitExprTo(val, target, b, paramMap, usedVars, callComment, pos); err != nil {
						return err
					}
					retOffset++
				}
			}
			// Zero remaining @retK slots
			totalRets := 0
			for k := range paramMap {
				if strings.HasPrefix(k, "@ret") {
					n, err := strconv.Atoi(k[4:])
					if err == nil && n > totalRets {
						totalRets = n
					}
				}
			}
			for i := retOffset + 1; i <= totalRets; i++ {
				target := resolveVarName("@ret"+strconv.Itoa(i), paramMap)
				f := map[string]any{"op": "set_reg", "1": false, "2": target}
				b.emit(f)
			}
			// Emit @return jump placeholder
			b.emit(map[string]any{
				"op": "@return",
			})
		}
	}
	return nil
}

// emitFnIfStmt emits an if/else if/else statement in a fn body using
// forward-jump patching.
func (p *parser) emitFnIfStmt(s *IfStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	// Collect all branches: condition + body pairs, plus optional else
	type branch struct {
		cond Expr
		body []Stmt
	}
	branches := []branch{{cond: s.Cond, body: s.Body}}
	for _, elif := range s.ElseIfs {
		branches = append(branches, branch{cond: elif.Cond, body: elif.Body})
	}

	// Track positions of jump-to-continuation frames that need patching
	var jumpsToPatch []int

	for i, br := range branches {
		brComment := ""
		if i == 0 {
			brComment = comment
		}

		// Emit condition check with placeholder false branch
		resolved, err := p.resolveFnBoolTree(br.cond, b, paramMap, usedVars, pos)
		if err != nil {
			return err
		}

		// For a single check, emit it inline. For chains, emit recursive tree.
		checkStart := b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount) // true body starts right after checks
		falsePlaceholder := frameRef(0)                 // patched later

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, brComment)
		}

		// Emit true body
		if err := p.emitFnBody(br.body, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}

		// If there's more branches or an else, emit jump-to-continuation
		hasMore := i < len(branches)-1 || len(s.Else) > 0
		if hasMore {
			jumpIdx := b.pos()
			b.emit(map[string]any{
				"op":   "set_reg",
				"1":    false,
				"2":    false,
				"next": frameRef(0), // patched to after all branches
			})
			jumpsToPatch = append(jumpsToPatch, jumpIdx)
		}

		patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, frameRef(b.pos()))
	}

	// Emit else body if present
	if len(s.Else) > 0 {
		if err := p.emitFnBody(s.Else, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}
	}

	// Patch all jumps-to-continuation to point to after everything
	afterAll := frameRef(b.pos())
	for _, idx := range jumpsToPatch {
		b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitFnIfExpr emits an if-expression in a fn body, writing the result to target.
func (p *parser) emitFnIfExpr(e *IfExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	type branch struct {
		cond Expr
		body []Stmt
		tail Expr
	}
	branches := []branch{{cond: e.Cond, body: e.Body, tail: e.Tail}}
	for _, elif := range e.ElseIfs {
		branches = append(branches, branch{cond: elif.Cond, body: elif.Body, tail: elif.Tail})
	}

	var jumpsToPatch []int

	for i, br := range branches {
		brComment := ""
		if i == 0 {
			brComment = comment
		}

		resolved, err := p.resolveFnBoolTree(br.cond, b, paramMap, usedVars, pos)
		if err != nil {
			return err
		}

		checkStart := b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, brComment)
		}

		// Emit body
		if err := p.emitFnBody(br.body, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}

		// Emit tail to target
		if err := p.emitExprTo(br.tail, target, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}

		// Jump-to-continuation
		jumpIdx := b.pos()
		b.emit(map[string]any{
			"op":   "set_reg",
			"1":    false,
			"2":    false,
			"next": frameRef(0),
		})
		jumpsToPatch = append(jumpsToPatch, jumpIdx)

		patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, frameRef(b.pos()))
	}

	// Else body + tail (or null for missing else)
	if e.ElsTail != nil {
		if err := p.emitFnBody(e.ElsBody, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}
		if err := p.emitExprTo(e.ElsTail, target, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}
	} else {
		// No else clause — assign null to target
		b.emit(map[string]any{
			"op": "set_reg",
			"1":  false,
			"2":  target,
		})
	}

	// Patch jumps
	afterAll := frameRef(b.pos())
	for _, idx := range jumpsToPatch {
		b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitFnIfExprMulti emits an if-expression with multi-return tails in a fn body.
func (p *parser) emitFnIfExprMulti(e *IfExpr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	type branch struct {
		cond Expr
		body []Stmt
		tail Expr
	}
	branches := []branch{{cond: e.Cond, body: e.Body, tail: e.Tail}}
	for _, elif := range e.ElseIfs {
		branches = append(branches, branch{cond: elif.Cond, body: elif.Body, tail: elif.Tail})
	}

	var jumpsToPatch []int

	for i, br := range branches {
		brComment := ""
		if i == 0 {
			brComment = comment
		}

		resolved, err := p.resolveFnBoolTree(br.cond, b, paramMap, usedVars, pos)
		if err != nil {
			return err
		}

		checkStart := b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, brComment)
		}

		if err := p.emitFnBody(br.body, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}

		// Emit tail to retVals
		if err := p.emitFnIfExprTailMulti(br.tail, retVals, b, paramMap, usedVars, pos); err != nil {
			return err
		}

		jumpIdx := b.pos()
		b.emit(map[string]any{
			"op":   "set_reg",
			"1":    false,
			"2":    false,
			"next": frameRef(0),
		})
		jumpsToPatch = append(jumpsToPatch, jumpIdx)

		patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, frameRef(b.pos()))
	}

	// Else body + tail (or null for missing else)
	if e.ElsTail != nil {
		if err := p.emitFnBody(e.ElsBody, b, paramMap, usedVars, "", pos); err != nil {
			return err
		}
		if err := p.emitFnIfExprTailMulti(e.ElsTail, retVals, b, paramMap, usedVars, pos); err != nil {
			return err
		}
	} else {
		// No else clause — zero all retVal slots
		for _, rv := range retVals {
			b.emit(map[string]any{
				"op": "set_reg",
				"1":  false,
				"2":  rv,
			})
		}
	}

	afterAll := frameRef(b.pos())
	for _, idx := range jumpsToPatch {
		b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitFnIfExprTailMulti emits a tail expression directing values to retVals in fn body context.
func (p *parser) emitFnIfExprTailMulti(tail Expr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, pos int) error {
	if ce, ok := tail.(*CallExpr); ok {
		resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(ce.Args, ce.KwArgs, b, paramMap, usedVars, pos)
		if err != nil {
			return err
		}
		return p.expandCall(ce.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, "", usedVars)
	}
	// Single-return tail: emit to first retVal, zero rest
	if err := p.emitExprTo(tail, retVals[0], b, paramMap, usedVars, "", pos); err != nil {
		return err
	}
	for i := 1; i < len(retVals); i++ {
		b.emit(map[string]any{"op": "set_reg", "1": false, "2": retVals[i]})
	}
	return nil
}

// emitFnWaitStmt emits a wait statement in a fn body.
func (p *parser) emitFnWaitStmt(s *WaitStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	// Resolve ticks expression
	ticksVal, err := p.emitExprGetValue(s.Ticks, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}

	if s.Tail == nil {
		// Simple wait: just emit the wait frame
		f := map[string]any{"op": "wait", "1": ticksVal}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	// Block wait: snapshot ticks if needed, then wait → body → cond → loop back

	// Snapshot: copy ticks to temp var so they're only evaluated once.
	// Skip for pure number literals (they can't change between iterations).
	ticksVar := ticksVal
	needsSnapshot := true
	if lit, ok := s.Ticks.(*LiteralExpr); ok {
		if _, isMap := lit.Value.(map[string]any); isMap {
			needsSnapshot = false // pure number literal like {"num": 5}
		}
	}
	if needsSnapshot {
		tmp := allocUniqueVar("@wait", usedVars)
		b.emit(map[string]any{
			"op": "set_reg",
			"1":  ticksVal,
			"2":  tmp,
		})
		ticksVar = tmp
	}

	// Emit wait frame
	waitFrame := map[string]any{"op": "wait", "1": ticksVar}
	setComment(waitFrame, comment)
	waitPos := b.emit(waitFrame)

	// Emit body
	if err := p.emitFnBody(s.Body, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// Emit tail condition to temp var
	condVar := allocUniqueVar("@wcond", usedVars)
	if err := p.emitExprTo(s.Tail, condVar, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// Emit truthy check: compare_register condVar, false
	// Different (truthy) → afterWait, Equal (falsy / next) → waitPos
	afterWait := frameRef(b.pos() + 1)
	b.emit(map[string]any{
		"op":                "compare_register",
		compareRegDifferent: afterWait,
		compareRegValue1:    condVar,
		compareRegValue2:    false,
		"next":              frameRef(waitPos),
	})

	return nil
}

// emitFnWhileStmt emits a while loop in a fn body.
func (p *parser) emitFnWhileStmt(s *WhileStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	loopStart := b.pos()

	// Emit condition check
	resolved, err := p.resolveFnBoolTree(s.Cond, b, paramMap, usedVars, pos)
	if err != nil {
		return err
	}

	checkStart := b.pos()
	checkCount := resolved.frameCount()
	trueBranch := frameRef(checkStart + checkCount)
	falsePlaceholder := frameRef(0)

	if resolved.isLeaf() {
		p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, comment)
	} else {
		p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, comment)
	}

	origLen := len(b.frames)

	// Emit body
	if err := p.emitFnBody(s.Body, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// Jump back to loop start.
	emitLoopBackEdge(b, loopStart, frameRef(loopStart))

	afterLoop := frameRef(b.pos())
	patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, afterLoop)

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitFnLoopStmt emits a loop/break in a fn body.
func (p *parser) emitFnLoopStmt(s *LoopStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	if s.Count != nil {
		return p.emitFnCountedLoop(s, b, paramMap, usedVars, comment, pos)
	}

	loopStart := b.pos()

	// Track break frame indices for patching
	origLen := len(b.frames)

	// Emit body
	if err := p.emitFnBody(s.Body, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// Jump back to loop start.
	emitLoopBackEdge(b, loopStart, frameRef(loopStart))

	afterLoop := frameRef(b.pos())
	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitFnCountedLoop emits a counted loop in a fn body.
func (p *parser) emitFnCountedLoop(s *LoopStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	counterVar := allocUniqueVar("@loop", usedVars)

	// Resolve count expression
	limit, err := p.emitExprGetValue(s.Count, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}

	// INIT: set_number 0 → counter
	b.emit(map[string]any{
		"op": "set_number",
		"2":  map[string]any{"num": 0},
		"3":  counterVar,
	})

	// CHECK: check_number counter vs limit
	checkFrame := b.emit(map[string]any{
		"op":        "check_number",
		checkValue:  counterVar,
		checkTarget: limit,
	})
	setComment(b.get(checkFrame), comment)

	// Track body start for @break scanning
	origLen := len(b.frames)

	// Emit body
	if err := p.emitFnBody(s.Body, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// INCR: add counter + 1 → counter, next → CHECK
	incrFrame := b.emit(map[string]any{
		"op":   "add",
		"1":    counterVar,
		"2":    map[string]any{"num": 1},
		"3":    counterVar,
		"next": frameRef(checkFrame),
	})

	// Set last body frame's "next" to incr
	patchLastBodyNext(b, origLen, incrFrame)

	// Patch CHECK exits: larger and equal → afterLoop
	afterLoop := frameRef(b.pos())
	check := b.get(checkFrame)
	check[checkLarger] = afterLoop
	check["next"] = afterLoop

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitFnForStmt emits a for-in loop in a fn body.
func (p *parser) emitFnForStmt(s *ForStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	counterVar := allocUniqueVar(s.IterVar, usedVars)
	paramMap[s.IterVar] = counterVar

	ctor, isCtor := s.Range.(*ConstructorExpr)
	if isCtor && ctor.TypeName == "Range" {
		return p.emitFnForStmtRange(s, ctor, counterVar, b, paramMap, usedVars, comment, pos)
	}
	return p.emitFnForStmtRuntime(s, counterVar, b, paramMap, usedVars, comment, pos)
}

func (p *parser) emitFnForStmtRange(s *ForStmt, ctor *ConstructorExpr, counterVar string, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	stepLit, stepIsLit := ctor.Args[2].(*LiteralExpr)
	var stepSign int
	if stepIsLit {
		if m, ok := stepLit.Value.(map[string]any); ok {
			if n, ok := m["num"].(int); ok {
				if n > 0 {
					stepSign = 1
				} else {
					stepSign = -1
				}
			}
		}
	}

	if !stepIsLit || stepSign == 0 {
		return p.emitFnForStmtRuntime(s, counterVar, b, paramMap, usedVars, comment, pos)
	}

	startVal, err := p.emitExprGetValue(ctor.Args[0], b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	stopVal, err := p.emitExprGetValue(ctor.Args[1], b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	stepVal, err := p.emitExprGetValue(ctor.Args[2], b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}

	// INIT
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  startVal,
		"2":  counterVar,
	})

	// CHECK
	check := map[string]any{
		"op":        "check_number",
		checkValue:  counterVar,
		checkTarget: stopVal,
	}
	setComment(check, comment)
	checkFrame := b.emit(check)

	origLen := len(b.frames)

	// Emit body
	if err := p.emitFnBody(s.Body, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// INCR
	incrFrame := b.emit(map[string]any{
		"op":   "add",
		"1":    counterVar,
		"2":    stepVal,
		"3":    counterVar,
		"next": frameRef(checkFrame),
	})

	// Set last body frame's "next" to incr
	patchLastBodyNext(b, origLen, incrFrame)

	afterLoop := frameRef(b.pos())
	if stepSign > 0 {
		check[checkLarger] = afterLoop
		check["next"] = afterLoop
	} else {
		check[checkSmaller] = afterLoop
		check["next"] = afterLoop
	}

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

func (p *parser) emitFnForStmtRuntime(s *ForStmt, counterVar string, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	rangeVal, err := p.emitExprGetValue(s.Range, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}

	stepVar := allocUniqueVar("@step", usedVars)
	startVar := allocUniqueVar("@start", usedVars)
	stopVar := allocUniqueVar("@stop", usedVars)
	retVals := []any{stepVar, false, false, startVar, stopVar}
	if err := p.expandCall("separate_register", []any{rangeVal}, nil, retVals, b, pos, "", usedVars); err != nil {
		return err
	}

	// INIT
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  startVar,
		"2":  counterVar,
	})

	// STEP_CHK
	stepCheck := map[string]any{
		"op":        "check_number",
		checkValue:  stepVar,
		checkTarget: map[string]any{"num": 0},
	}
	setComment(stepCheck, comment)
	stepCheckFrame := b.emit(stepCheck)

	// CHECK_POS
	checkPos := map[string]any{
		"op":        "check_number",
		checkValue:  counterVar,
		checkTarget: stopVar,
	}
	checkPosFrame := b.emit(checkPos)

	// CHECK_NEG
	checkNeg := map[string]any{
		"op":        "check_number",
		checkValue:  counterVar,
		checkTarget: stopVar,
	}
	checkNegFrame := b.emit(checkNeg)

	origLen := len(b.frames)

	// Emit body
	if err := p.emitFnBody(s.Body, b, paramMap, usedVars, "", pos); err != nil {
		return err
	}

	// INCR
	incrFrame := b.emit(map[string]any{
		"op":   "add",
		"1":    counterVar,
		"2":    stepVar,
		"3":    counterVar,
		"next": frameRef(stepCheckFrame),
	})

	patchLastBodyNext(b, origLen, incrFrame)

	afterLoop := frameRef(b.pos())

	stepCheck[checkLarger] = frameRef(checkPosFrame)
	stepCheck[checkSmaller] = frameRef(checkNegFrame)
	stepCheck["next"] = afterLoop

	bodyStart := frameRef(origLen)
	checkPos[checkSmaller] = bodyStart
	checkPos[checkLarger] = afterLoop
	checkPos["next"] = afterLoop

	checkNeg[checkLarger] = bodyStart
	checkNeg[checkSmaller] = afterLoop
	checkNeg["next"] = afterLoop

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

func (p *parser) parseParamList() ([]paramDef, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	var params []paramDef
	seenKeyword := false
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRParen {
			break
		}
		if len(params) > 0 {
			if tok.kind != tokComma {
				return nil, p.errorf(tok.pos, "expected ',' or ')', got %s", tok.describe())
			}
			tok, err = p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected parameter name, got %s", tok.describe())
		}

		// Check for direction annotation (in, out, inout)
		direction := ""
		if isDirection(tok.val) {
			direction = tok.val
			tok, err = p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
		}

		// Peek: if next is an identifier, this is a keyword param
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent {
			// keyword param: tok is keyword, peek is variable name
			seenKeyword = true
			params = append(params, paramDef{
				name: peek.val, keyword: tok.val, direction: direction,
			})
		} else {
			// positional param
			p.unget(peek)
			if seenKeyword {
				return nil, p.errorf(tok.pos, "positional parameter after keyword parameter")
			}
			params = append(params, paramDef{name: tok.val, direction: direction})
		}
	}
	return params, nil
}

func parseStdlibFile(src string, fns map[string]*fnDef) error {
	p := &parser{scanner: scanner{src: src}, fns: fns}
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return nil
		}
		if tok.kind != tokIdent || tok.val != "fn" {
			return p.errorf(tok.pos, "expected 'fn', got %s", tok.describe())
		}
		if _, err := p.parseUserFn(); err != nil {
			return err
		}
	}
}

// --- Two-pass file parsing ---

func (p *parser) parseBehaviorID() (token, error) {
	tok, err := p.next()
	if err != nil {
		return tok, err
	}
	if tok.kind != tokIdent && tok.kind != tokString {
		return tok, p.errorf(tok.pos, "expected behavior id, got %s", tok.describe())
	}
	return tok, nil
}

// --- Compile-time constants ---

// parseConstDecl parses a const declaration after the `const` keyword.
// Syntax: const <name> = <expr>
// Returns the constant name.
func (p *parser) parseConstDecl(private bool) (string, error) {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	name := nameTok.val
	if Keywords[name] {
		return "", p.errorf(nameTok.pos, "%q is a reserved keyword and cannot be used as a constant name", name)
	}
	if name == "_" {
		return "", p.errorf(nameTok.pos, "'_' cannot be used as a constant name")
	}
	if _, ok := p.consts[name]; ok {
		return "", p.errorf(nameTok.pos, "duplicate constant %q", name)
	}
	if _, ok := p.fns[name]; ok {
		return "", p.errorf(nameTok.pos, "constant %q conflicts with a function of the same name", name)
	}
	if _, err := p.expect(tokEquals); err != nil {
		return "", err
	}

	// Peek to see if the RHS is a string or localize block (these can't go through parseBoolExpr)
	peek, err := p.next()
	if err != nil {
		return "", err
	}

	var val any
	if peek.kind == tokString {
		val = peek.val
	} else if peek.kind == tokIdent && peek.val == "localize" {
		resolved, err := p.parseLocalize()
		if err != nil {
			return "", err
		}
		val = resolved
	} else {
		p.unget(peek)

		// Parse expression using a const-only resolver
		constResolver := func(tok token) (Expr, error) {
			if strings.HasPrefix(tok.val, "$") {
				if reg, ok := unitRegisters[tok.val]; ok {
					return &LiteralExpr{Value: reg}, nil
				}
				return nil, p.errorf(tok.pos, "unknown unit register %q", tok.val)
			}
			if c, ok := p.consts[tok.val]; ok {
				return &LiteralExpr{Value: c.value}, nil
			}
			return nil, p.errorf(tok.pos, "%q is not a compile-time constant", tok.val)
		}
		// Enable function calls in const expressions
		savedCallExprParser := p.callExprParser
		p.callExprParser = func(callee *fnDef, calleeTok token) (Expr, error) {
			args, kwArgs, err := p.parseConstCallArgs(callee, calleeTok, constResolver)
			if err != nil {
				return nil, err
			}
			return &CallExpr{Name: calleeTok.val, Args: args, KwArgs: kwArgs}, nil
		}
		defer func() { p.callExprParser = savedCallExprParser }()

		expr, err := p.parseBoolExpr(constResolver)
		if err != nil {
			return "", err
		}

		// Unwrap TruthyExpr for plain arithmetic results (same pattern as
		// parseBhvVarInit and parseFnBodyRHSExpr)
		if te, ok := expr.(*TruthyExpr); ok {
			expr = te.Value
		}

		// Check for & operator after the expression
		ampTok, err := p.next()
		if err != nil {
			return "", err
		}
		if ampTok.kind == tokAmpersand {
			numExpr, err := p.parseBoolExpr(constResolver)
			if err != nil {
				return "", err
			}
			if te, ok := numExpr.(*TruthyExpr); ok {
				numExpr = te.Value
			}
			expr = &AmpersandExpr{Value: expr, Num: numExpr}
		} else {
			p.unget(ampTok)
		}

		// Evaluate the expression using the compile-time evaluator
		p.evalStepLimit = 10000
		result, ok := p.tryEvalExpr(expr, nil)
		if !ok {
			return "", p.errorf(nameTok.pos, "expression is not compile-time evaluable")
		}
		val = result
	}

	p.consts[name] = &constDef{value: val, private: private}
	return name, nil
}

// --- Compile-time evaluator ---
//
// tryEvalExpr, tryEvalCall, and tryEvalStmts form a compile-time evaluator
// that can trace through function calls. The evaluator bails (returns false)
// when it encounters runtime-only constructs like the instruction intrinsic
// or wait statements. It uses p.evalStepLimit as a safety limit.

// constEvalStatus signals non-normal completion from tryEvalStmts.
type constEvalStatus struct {
	returned   bool
	retVals    []any
	broke      bool
	breakLabel string
}

// tryEvalExpr evaluates an expression at compile time.
// env holds local variable bindings (nil for top-level const expressions).
// Returns (value, true) on success, (nil, false) on bail.
func (p *parser) tryEvalExpr(expr Expr, env map[string]any) (any, bool) {
	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Value, true
	case *IdentExpr:
		if env != nil {
			if val, ok := env[e.Name]; ok {
				return val, true
			}
		}
		return nil, false
	case *ArithExpr:
		lhs, ok := p.tryEvalExpr(e.LHS, env)
		if !ok {
			return nil, false
		}
		rhs, ok := p.tryEvalExpr(e.RHS, env)
		if !ok {
			return nil, false
		}
		lNum, lOk := extractNum(lhs)
		rNum, rOk := extractNum(rhs)
		if !lOk || !rOk {
			return nil, false
		}
		var result int
		switch e.Op {
		case tokPlus:
			result = lNum + rNum
		case tokMinus:
			result = lNum - rNum
		case tokStar:
			result = lNum * rNum
		case tokSlash:
			if rNum == 0 {
				return nil, false
			}
			result = lNum / rNum
		case tokPercent:
			if rNum == 0 {
				return nil, false
			}
			result = lNum % rNum
		default:
			return nil, false
		}
		return map[string]any{"num": result}, true
	case *ConstructorExpr:
		// Evaluate constructor args first (they may reference env vars)
		resolved := &ConstructorExpr{TypeName: e.TypeName, Args: make([]Expr, len(e.Args))}
		for i, arg := range e.Args {
			val, ok := p.tryEvalExpr(arg, env)
			if !ok {
				return nil, false
			}
			resolved.Args[i] = &LiteralExpr{Value: val}
		}
		val, ok := tryResolveConstructorLiteral(resolved)
		if !ok {
			return nil, false
		}
		return val, true
	case *AmpersandExpr:
		// Evaluate both sides first
		lhs, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		rhs, ok := p.tryEvalExpr(e.Num, env)
		if !ok {
			return nil, false
		}
		resolved := &AmpersandExpr{
			Value: &LiteralExpr{Value: lhs},
			Num:   &LiteralExpr{Value: rhs},
		}
		val, ok := tryResolveAmpersandLiteral(resolved)
		if !ok {
			return nil, false
		}
		return val, true
	case *CompareExpr:
		lhs, ok := p.tryEvalExpr(e.LHS, env)
		if !ok {
			return nil, false
		}
		rhs, ok := p.tryEvalExpr(e.RHS, env)
		if !ok {
			return nil, false
		}
		if evalCompare(e.Op, lhs, rhs) {
			return map[string]any{"num": 1}, true
		}
		return false, true
	case *BoolChainExpr:
		if e.Op == tokDoubleAmpersand {
			for _, child := range e.Children {
				val, ok := p.tryEvalExpr(child, env)
				if !ok {
					return nil, false
				}
				if !isTruthy(val) {
					return false, true
				}
			}
			return map[string]any{"num": 1}, true
		}
		// ||
		for _, child := range e.Children {
			val, ok := p.tryEvalExpr(child, env)
			if !ok {
				return nil, false
			}
			if isTruthy(val) {
				return map[string]any{"num": 1}, true
			}
		}
		return false, true
	case *NotExpr:
		inner, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		if isTruthy(inner) {
			return false, true
		}
		return map[string]any{"num": 1}, true
	case *TruthyExpr:
		inner, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		if isTruthy(inner) {
			return map[string]any{"num": 1}, true
		}
		return false, true
	case *TypeCheckExpr:
		val, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		if evalTypeCheck(val, e.TypeSlot) {
			return map[string]any{"num": 1}, true
		}
		return false, true
	case *CallExpr:
		fn := p.fns[e.Name]
		if fn == nil {
			return nil, false
		}
		// Evaluate positional args
		posArgs, kwArgs, ok := p.tryEvalCallArgs(e.Args, e.KwArgs, env)
		if !ok {
			return nil, false
		}
		retVals, ok := p.tryEvalCall(fn, posArgs, kwArgs)
		if !ok {
			return nil, false
		}
		if len(retVals) == 0 {
			return false, true
		}
		return retVals[0], true
	case *ModeBlockExpr:
		// Mode is irrelevant at compile time; just eval body + tail
		status, ok := p.tryEvalStmts(e.Body, env)
		if !ok {
			return nil, false
		}
		if status != nil {
			return nil, false // unexpected return/break in mode block body
		}
		return p.tryEvalExpr(e.Tail, env)
	case *IfExpr:
		cond, ok := p.tryEvalExpr(e.Cond, env)
		if !ok {
			return nil, false
		}
		if isTruthy(cond) {
			status, ok := p.tryEvalStmts(e.Body, env)
			if !ok {
				return nil, false
			}
			if status != nil {
				return nil, false
			}
			return p.tryEvalExpr(e.Tail, env)
		}
		for _, elif := range e.ElseIfs {
			cond, ok := p.tryEvalExpr(elif.Cond, env)
			if !ok {
				return nil, false
			}
			if isTruthy(cond) {
				status, ok := p.tryEvalStmts(elif.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					return nil, false
				}
				return p.tryEvalExpr(elif.Tail, env)
			}
		}
		if e.ElsTail != nil {
			status, ok := p.tryEvalStmts(e.ElsBody, env)
			if !ok {
				return nil, false
			}
			if status != nil {
				return nil, false
			}
			return p.tryEvalExpr(e.ElsTail, env)
		}
		return false, true // no else → null
	case *ExprListExpr:
		// Evaluate each expression; collect results
		var results []any
		for _, sub := range e.Exprs {
			val, ok := p.tryEvalExpr(sub, env)
			if !ok {
				return nil, false
			}
			results = append(results, val)
		}
		if len(results) == 1 {
			return results[0], true
		}
		return results, true
	case *InstructionExpr:
		return nil, false // bail: runtime-only
	default:
		return nil, false
	}
}

// tryEvalCallArgs evaluates positional and keyword arguments at compile time.
// Returns (posArgs, kwArgs, true) on success, (nil, nil, false) if any arg bails.
func (p *parser) tryEvalCallArgs(args []Expr, kwArgExprs map[string]Expr, env map[string]any) ([]any, map[string]any, bool) {
	posArgs := make([]any, len(args))
	for i, arg := range args {
		val, ok := p.tryEvalExpr(arg, env)
		if !ok {
			return nil, nil, false
		}
		posArgs[i] = val
	}
	var kwArgs map[string]any
	if len(kwArgExprs) > 0 {
		kwArgs = make(map[string]any, len(kwArgExprs))
		for k, v := range kwArgExprs {
			val, ok := p.tryEvalExpr(v, env)
			if !ok {
				return nil, nil, false
			}
			kwArgs[k] = val
		}
	}
	return posArgs, kwArgs, true
}

// tryEvalCall evaluates a function call at compile time.
// Returns (returnValues, true) on success, (nil, false) on bail.
func (p *parser) tryEvalCall(fn *fnDef, posArgs []any, kwArgs map[string]any) ([]any, bool) {
	if fn.frame != nil {
		return nil, false // instruction-based function → bail
	}
	if fn.astBody == nil {
		return nil, false // no body → bail
	}

	// Build environment from params + args
	env := map[string]any{}
	posIdx := 0
	for _, param := range fn.params {
		if param.keyword == "" {
			if posIdx < len(posArgs) {
				env[param.name] = posArgs[posIdx]
			} else {
				env[param.name] = false
			}
			posIdx++
		} else {
			if kwArgs != nil {
				if val, ok := kwArgs[param.keyword]; ok {
					env[param.name] = val
				} else {
					env[param.name] = false
				}
			} else {
				env[param.name] = false
			}
		}
	}

	// Merge transitive function scope (same pattern as expandCall)
	var savedFns map[string]*fnDef
	if fn.scope != nil {
		savedFns = map[string]*fnDef{}
		for name, def := range fn.scope {
			if _, exists := p.fns[name]; !exists {
				p.fns[name] = def
				savedFns[name] = def
			}
		}
	}

	status, ok := p.tryEvalStmts(fn.astBody, env)

	// Restore function scope
	if savedFns != nil {
		for name := range savedFns {
			delete(p.fns, name)
		}
	}

	if !ok {
		return nil, false
	}

	// Extract return values
	if status != nil && status.returned {
		return status.retVals, true
	}

	// No explicit return — extract from rets
	if fn.rets != nil {
		retVals := make([]any, len(fn.rets))
		for i, name := range fn.rets {
			if val, ok := env[name]; ok {
				retVals[i] = val
			} else {
				retVals[i] = false
			}
		}
		return retVals, true
	}

	return nil, true
}

// tryEvalStmts evaluates a list of statements at compile time.
// Returns (nil, true) for normal completion, (*constEvalStatus, true) for
// return/break, and (nil, false) for bail.
func (p *parser) tryEvalStmts(stmts []Stmt, env map[string]any) (*constEvalStatus, bool) {
	for _, stmt := range stmts {
		p.evalStepLimit--
		if p.evalStepLimit <= 0 {
			return nil, false // step limit exceeded
		}
		switch s := stmt.(type) {
		case *LetStmt:
			val, ok := p.tryEvalExpr(s.Value, env)
			if !ok {
				return nil, false
			}
			env[s.Name] = val
		case *AssignStmt:
			val, ok := p.tryEvalExpr(s.Value, env)
			if !ok {
				return nil, false
			}
			env[s.Target] = val
		case *CompoundAssignStmt:
			rhs, ok := p.tryEvalExpr(s.Value, env)
			if !ok {
				return nil, false
			}
			lhs, ok := env[s.Target]
			if !ok {
				return nil, false
			}
			lNum, lOk := extractNum(lhs)
			rNum, rOk := extractNum(rhs)
			if !lOk || !rOk {
				return nil, false
			}
			var result int
			switch s.Op {
			case tokPlusEquals:
				result = lNum + rNum
			case tokMinusEquals:
				result = lNum - rNum
			case tokStarEquals:
				result = lNum * rNum
			case tokSlashEquals:
				if rNum == 0 {
					return nil, false
				}
				result = lNum / rNum
			case tokPercentEquals:
				if rNum == 0 {
					return nil, false
				}
				result = lNum % rNum
			default:
				return nil, false
			}
			env[s.Target] = map[string]any{"num": result}
		case *IncrDecrStmt:
			lhs, ok := env[s.Target]
			if !ok {
				return nil, false
			}
			lNum, lOk := extractNum(lhs)
			if !lOk {
				return nil, false
			}
			if s.Op == tokPlusPlus {
				env[s.Target] = map[string]any{"num": lNum + 1}
			} else {
				env[s.Target] = map[string]any{"num": lNum - 1}
			}
		case *MultiReturnStmt:
			// Evaluate RHS
			switch rv := s.Value.(type) {
			case *CallExpr:
				fn := p.fns[rv.Name]
				if fn == nil {
					return nil, false
				}
				posArgs, kwArgs, ok := p.tryEvalCallArgs(rv.Args, rv.KwArgs, env)
				if !ok {
					return nil, false
				}
				retVals, ok := p.tryEvalCall(fn, posArgs, kwArgs)
				if !ok {
					return nil, false
				}
				for i, b := range s.Bindings {
					if b.Discard {
						continue
					}
					if i < len(retVals) {
						env[b.Name] = retVals[i]
					} else {
						env[b.Name] = false
					}
				}
			case *ExprListExpr:
				idx := 0
				for _, e := range rv.Exprs {
					val, ok := p.tryEvalExpr(e, env)
					if !ok {
						return nil, false
					}
					if idx < len(s.Bindings) {
						b := s.Bindings[idx]
						if !b.Discard {
							env[b.Name] = val
						}
						idx++
					}
				}
			default:
				return nil, false
			}
		case *CallStmt:
			fn := p.fns[s.Name]
			if fn == nil {
				return nil, false
			}
			posArgs, kwArgs, ok := p.tryEvalCallArgs(s.Args, s.KwArgs, env)
			if !ok {
				return nil, false
			}
			_, ok = p.tryEvalCall(fn, posArgs, kwArgs)
			if !ok {
				return nil, false
			}
		case *ReturnStmt:
			var retVals []any
			for _, v := range s.Values {
				val, ok := p.tryEvalExpr(v, env)
				if !ok {
					return nil, false
				}
				retVals = append(retVals, val)
			}
			return &constEvalStatus{returned: true, retVals: retVals}, true
		case *IfStmt:
			cond, ok := p.tryEvalExpr(s.Cond, env)
			if !ok {
				return nil, false
			}
			if isTruthy(cond) {
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					return status, true
				}
				continue
			}
			matched := false
			for _, elif := range s.ElseIfs {
				cond, ok := p.tryEvalExpr(elif.Cond, env)
				if !ok {
					return nil, false
				}
				if isTruthy(cond) {
					status, ok := p.tryEvalStmts(elif.Body, env)
					if !ok {
						return nil, false
					}
					if status != nil {
						return status, true
					}
					matched = true
					break
				}
			}
			if !matched && s.Else != nil {
				status, ok := p.tryEvalStmts(s.Else, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					return status, true
				}
			}
		case *LoopStmt:
			if s.Count == nil {
				return nil, false // infinite loop → bail
			}
			countVal, ok := p.tryEvalExpr(s.Count, env)
			if !ok {
				return nil, false
			}
			countNum, cOk := extractNum(countVal)
			if !cOk || countNum < 0 {
				return nil, false
			}
			for i := 0; i < countNum; i++ {
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					if status.broke {
						if status.breakLabel == "" || status.breakLabel == s.Label {
							break // break consumed
						}
						return status, true // propagate labeled break
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *WhileStmt:
			for {
				p.evalStepLimit--
				if p.evalStepLimit <= 0 {
					return nil, false
				}
				cond, ok := p.tryEvalExpr(s.Cond, env)
				if !ok {
					return nil, false
				}
				if !isTruthy(cond) {
					break
				}
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					if status.broke {
						if status.breakLabel == "" || status.breakLabel == s.Label {
							break
						}
						return status, true
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *ForStmt:
			rangeVal, ok := p.tryEvalExpr(s.Range, env)
			if !ok {
				return nil, false
			}
			// Extract range parts from coordinate+number composite
			rm, ok := rangeVal.(map[string]any)
			if !ok {
				return nil, false
			}
			coord, ok := rm["coord"].(map[string]any)
			if !ok {
				return nil, false
			}
			start, ok := coord["x"].(int)
			if !ok {
				return nil, false
			}
			stop, ok := coord["y"].(int)
			if !ok {
				return nil, false
			}
			step := 1
			if n, ok := rm["num"]; ok {
				if s, ok := n.(int); ok && s != 0 {
					step = s
				}
			}
			for i := start; (step > 0 && i < stop) || (step < 0 && i > stop); i += step {
				p.evalStepLimit--
				if p.evalStepLimit <= 0 {
					return nil, false
				}
				env[s.IterVar] = map[string]any{"num": i}
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					if status.broke {
						if status.breakLabel == "" || status.breakLabel == s.Label {
							break
						}
						return status, true
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *BreakStmt:
			return &constEvalStatus{broke: true, breakLabel: s.Label}, true
		case *ModeBlockStmt:
			// Mode is irrelevant at compile time
			status, ok := p.tryEvalStmts(s.Body, env)
			if !ok {
				return nil, false
			}
			if status != nil {
				return status, true
			}
		case *InstructionStmt:
			return nil, false // bail: runtime-only
		case *WaitStmt:
			return nil, false // bail: runtime-only
		default:
			return nil, false
		}
	}
	return nil, true // normal completion
}

// parseConstCallArgs parses function call arguments in a const expression context.
func (p *parser) parseConstCallArgs(callee *fnDef, calleeTok token, constResolver operandResolver) ([]Expr, map[string]Expr, error) {
	// Detect parenthesized call syntax
	paren := false
	peek, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if peek.kind == tokLParen {
		paren = true
	} else {
		p.unget(peek)
	}

	posCount := callee.positionalCount()
	args := make([]Expr, posCount)
	for i := 0; i < posCount; i++ {
		if i > 0 {
			if paren {
				if _, err := p.expect(tokComma); err != nil {
					return nil, nil, err
				}
			} else {
				sep, err := p.next()
				if err != nil {
					return nil, nil, err
				}
				if sep.kind != tokComma {
					p.unget(sep)
				}
			}
		}

		// Skip direction annotations (irrelevant for compile-time eval)
		dirTok, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		if !(dirTok.kind == tokIdent && isDirection(dirTok.val)) {
			p.unget(dirTok)
		}

		val, err := p.parseConstArgExpr(constResolver)
		if err != nil {
			return nil, nil, err
		}
		args[i] = val
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err = p.next()
	if err != nil {
		return nil, nil, err
	}
	if peek.kind == tokComma && callee.positionalCount() < len(callee.params) {
		kwArgs = map[string]Expr{}
		for {
			dirOrKw, err := p.expect(tokIdent)
			if err != nil {
				return nil, nil, err
			}
			if isDirection(dirOrKw.val) {
				dirOrKw, err = p.expect(tokIdent)
				if err != nil {
					return nil, nil, err
				}
			}
			kw := callee.keywordByName(dirOrKw.val)
			if kw == nil {
				return nil, nil, p.errorf(dirOrKw.pos, "unknown keyword argument %q", dirOrKw.val)
			}
			if _, exists := kwArgs[dirOrKw.val]; exists {
				return nil, nil, p.errorf(dirOrKw.pos, "duplicate keyword argument %q", dirOrKw.val)
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, nil, err
			}
			val, err := p.parseConstArgExpr(constResolver)
			if err != nil {
				return nil, nil, err
			}
			kwArgs[dirOrKw.val] = val

			next, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			if next.kind != tokComma {
				p.unget(next)
				break
			}
		}
	} else {
		p.unget(peek)
	}

	if paren {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, nil, err
		}
	}

	return args, kwArgs, nil
}

// parseConstArgExpr parses a single argument in a const expression context.
func (p *parser) parseConstArgExpr(constResolver operandResolver) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokString {
		return &LiteralExpr{Value: tok.val}, nil
	}
	if tok.kind == tokIdent && tok.val == "localize" {
		resolved, err := p.parseLocalize()
		if err != nil {
			return nil, err
		}
		return &LiteralExpr{Value: resolved}, nil
	}
	p.unget(tok)
	expr, err := p.parseArithExpr(constResolver)
	if err != nil {
		return nil, err
	}
	// Check for & operator
	ampTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if ampTok.kind == tokAmpersand {
		numExpr, err := p.parseArithExpr(constResolver)
		if err != nil {
			return nil, err
		}
		return &AmpersandExpr{Value: expr, Num: numExpr}, nil
	}
	p.unget(ampTok)
	return expr, nil
}

// extractNum extracts the integer from a compile-time value.
func extractNum(val any) (int, bool) {
	if m, ok := val.(map[string]any); ok {
		if n, ok := m["num"]; ok {
			if i, ok := n.(int); ok {
				return i, true
			}
		}
	}
	return 0, false
}

// isTruthy checks if a compile-time value is truthy (non-false/non-null).
func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return true // maps, ints, strings are truthy
}

// evalCompare evaluates a comparison between two compile-time values.
func evalCompare(op tokenKind, lhs, rhs any) bool {
	if isEqualityOp(op) {
		eq := reflect.DeepEqual(lhs, rhs)
		if op == tokDoubleEquals {
			return eq
		}
		return !eq
	}
	lNum, lOk := extractNum(lhs)
	rNum, rOk := extractNum(rhs)
	if !lOk || !rOk {
		return false
	}
	switch op {
	case tokGreater:
		return lNum > rNum
	case tokGreaterEquals:
		return lNum >= rNum
	case tokLess:
		return lNum < rNum
	case tokLessEquals:
		return lNum <= rNum
	}
	return false
}

// evalTypeCheck checks whether a compile-time value matches a type slot.
func evalTypeCheck(val any, typeSlot string) bool {
	m, ok := val.(map[string]any)
	if !ok {
		return false
	}
	if _, hasID := m["id"]; hasID {
		id := m["id"].(string)
		switch typeSlot {
		case valueTypeItem:
			return !strings.HasPrefix(id, "c_") && !strings.HasPrefix(id, "t_") && !strings.HasPrefix(id, "v_")
		case valueTypeComp:
			return strings.HasPrefix(id, "c_")
		case valueTypeTech:
			return strings.HasPrefix(id, "t_")
		case valueTypeValue:
			return strings.HasPrefix(id, "v_")
		}
		return false
	}
	if _, hasCoord := m["coord"]; hasCoord {
		return typeSlot == valueTypeCoord
	}
	return false
}

func (p *parser) parseFile() (*codec.Object, error) {
	// Pass 1: collect user-defined function definitions
	if err := p.collectUserFns(); err != nil {
		return nil, err
	}

	// Validate behavior selection
	switch {
	case len(p.behaviorIDs) == 0:
		return nil, fmt.Errorf("source contains no behavior declarations")
	case p.target == "" && len(p.behaviorIDs) == 1:
		p.target = p.behaviorIDs[0] // auto-select
	case p.target == "":
		return nil, fmt.Errorf("source contains multiple behaviors; use -b to select one: %s",
			strings.Join(p.behaviorIDs, ", "))
	default:
		found := false
		for _, id := range p.behaviorIDs {
			if id == p.target {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("behavior %q not found; available behaviors: %s",
				p.target, strings.Join(p.behaviorIDs, ", "))
		}
	}

	// Pass 2: find and compile the behavior
	p.pos = 0
	p.ungot = nil
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokEOF {
			return nil, nil
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected declaration, got %s", tok.describe())
		}
		switch tok.val {
		case "behavior":
			idTok, err := p.parseBehaviorID()
			if err != nil {
				return nil, err
			}
			if idTok.val == p.target {
				return p.parseBehaviorBody(idTok.val)
			}
			// Skip non-matching behavior
			if err := p.skipBraceBlock(); err != nil {
				return nil, err
			}
		case "private":
			fnTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			switch fnTok.val {
			case "fn":
				if err := p.skipFnDef(); err != nil {
					return nil, err
				}
			case "const":
				if err := p.skipToNextDecl(); err != nil {
					return nil, err
				}
			default:
				return nil, p.errorf(fnTok.pos, "expected 'fn' or 'const' after 'private', got %q", fnTok.val)
			}
		case "fn":
			if err := p.skipFnDef(); err != nil {
				return nil, err
			}
		case "const":
			// Skip const declarations in pass 2 (already processed in pass 1)
			if err := p.skipToNextDecl(); err != nil {
				return nil, err
			}
		case "import":
			// Skip import statements in pass 2 (already processed in pass 1)
			if err := p.skipToNextDecl(); err != nil {
				return nil, err
			}
		default:
			return nil, p.errorf(tok.pos, "expected 'behavior', 'fn', 'const', or 'private', got %q", tok.val)
		}
	}
}

func (p *parser) collectUserFns() error {
	// Parse import statements at the top of the file
	if err := p.parseImports(); err != nil {
		return err
	}

	// Resolve imports and merge imported functions
	if err := p.processImports(); err != nil {
		return err
	}

	// Track same-file names for import collision checking
	var sameFileFns []string
	var sameFileConsts []string

	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			break
		}
		if tok.kind != tokIdent {
			return p.errorf(tok.pos, "expected declaration, got %s", tok.describe())
		}
		switch tok.val {
		case "behavior":
			idTok, err := p.parseBehaviorID()
			if err != nil {
				return err
			}
			p.behaviorIDs = append(p.behaviorIDs, idTok.val)
			if err := p.skipBraceBlock(); err != nil {
				return err
			}
		case "private":
			fnTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			switch fnTok.val {
			case "fn":
				name, err := p.parseUserFn()
				if err != nil {
					return err
				}
				p.fns[name].private = true
				sameFileFns = append(sameFileFns, name)
			case "const":
				name, err := p.parseConstDecl(true)
				if err != nil {
					return err
				}
				sameFileConsts = append(sameFileConsts, name)
			default:
				return p.errorf(fnTok.pos, "expected 'fn' or 'const' after 'private', got %q", fnTok.val)
			}
		case "fn":
			name, err := p.parseUserFn()
			if err != nil {
				return err
			}
			sameFileFns = append(sameFileFns, name)
		case "const":
			name, err := p.parseConstDecl(false)
			if err != nil {
				return err
			}
			sameFileConsts = append(sameFileConsts, name)
		case "import":
			return p.errorf(tok.pos, "import statements must appear before function and behavior declarations")
		default:
			return p.errorf(tok.pos, "expected 'behavior', 'fn', 'const', or 'private', got %q", tok.val)
		}
	}

	// Check for collisions between same-file names and imports
	return p.checkImportCollisions(sameFileFns, sameFileConsts)
}

// fnBodyContext holds the shared state for fn body parsing.
type fnVarInfo struct {
	mutable bool
	depth   int
	used    bool
}

type fnBodyContext struct {
	paramDirs    map[string]string    // param name -> effective direction
	fnVarInfo    map[string]fnVarInfo // name -> var info (mutability, depth, used tracking)
	fnScopeDepth int                  // current nesting depth (0 = fn top-level)
	resolve      operandResolver
}

// pushFnScope saves the current fnVarInfo map and increments scope depth.
func (ctx *fnBodyContext) pushFnScope() (map[string]fnVarInfo, int) {
	savedInfo := make(map[string]fnVarInfo, len(ctx.fnVarInfo))
	for k, v := range ctx.fnVarInfo {
		savedInfo[k] = v
	}
	depth := ctx.fnScopeDepth
	ctx.fnScopeDepth++
	return savedInfo, depth
}

// popFnScope restores fnVarInfo from a saved copy and decrements scope depth.
func (ctx *fnBodyContext) popFnScope(savedInfo map[string]fnVarInfo, depth int) {
	ctx.fnVarInfo = savedInfo
	ctx.fnScopeDepth = depth
}

// declareFnVar registers a variable at the current fn scope depth.
func (ctx *fnBodyContext) declareFnVar(name string, mutable bool) {
	ctx.fnVarInfo[name] = fnVarInfo{mutable: mutable, depth: ctx.fnScopeDepth}
}

// declareFnVarWarn is like declareFnVar but also emits a warning if the name
// already exists at the same depth and was never used.
func (ctx *fnBodyContext) declareFnVarWarn(name string, mutable bool, p *parser, pos int) {
	if existing, ok := ctx.fnVarInfo[name]; ok {
		if existing.depth == ctx.fnScopeDepth && !existing.used {
			p.warnf(pos, "variable %q shadows a previous declaration in the same scope that was never used", name)
		}
	}
	ctx.declareFnVar(name, mutable)
}

// markFnVarUsed marks a fn body variable as used for shadowing warnings.
func (ctx *fnBodyContext) markFnVarUsed(name string) {
	if info, ok := ctx.fnVarInfo[name]; ok {
		info.used = true
		ctx.fnVarInfo[name] = info
	}
}

// markExprUsed marks any IdentExpr variable as used for shadowing warnings.
func (ctx *fnBodyContext) markExprUsed(expr Expr) {
	if ident, ok := expr.(*IdentExpr); ok {
		ctx.markFnVarUsed(ident.Name)
	}
}

// canAssign checks whether name can be written to in a fn body context.
func (ctx *fnBodyContext) canAssign(name string, p *parser, pos int) error {
	if info, ok := ctx.fnVarInfo[name]; ok {
		if !info.mutable {
			return p.errorf(pos, "cannot assign to immutable variable %q", name)
		}
		return nil
	}
	if dir, ok := ctx.paramDirs[name]; ok {
		if dir == "in" {
			return p.errorf(pos, "cannot assign to input parameter %q", name)
		}
		return nil
	}
	return p.errorf(pos, "undeclared variable %q", name)
}

// canRead checks whether name can be read from in a fn body context
// for compound assignment (needs both read+write).
func (ctx *fnBodyContext) canCompound(name string, p *parser, pos int) error {
	if err := ctx.canAssign(name, p, pos); err != nil {
		return err
	}
	// Mark as used — compound assignment reads the variable
	ctx.markFnVarUsed(name)
	if dir, ok := ctx.paramDirs[name]; ok && dir == "out" {
		return p.errorf(pos, "cannot read from output parameter %q", name)
	}
	return nil
}

func (p *parser) parseUserFn() (string, error) {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	if Keywords[nameTok.val] {
		return "", p.errorf(nameTok.pos, "%q is a reserved keyword and cannot be used as a function name", nameTok.val)
	}

	params, err := p.parseParamList()
	if err != nil {
		return "", err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return "", err
	}

	// Build direction maps for enforcement in fn body
	paramDirs := map[string]string{} // param name -> effective direction
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
	}
	ctx := &fnBodyContext{
		paramDirs: paramDirs,
		fnVarInfo: map[string]fnVarInfo{},
	}
	ctx.resolve = p.fnBodyResolver(ctx)

	// Enable function calls in boolean primary position (e.g., d || my_fn x)
	prevCallExprParser := p.callExprParser
	p.callExprParser = func(callee *fnDef, calleeTok token) (Expr, error) {
		args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, ctx)
		if err != nil {
			return nil, err
		}
		return &CallExpr{Name: calleeTok.val, Args: args, KwArgs: kwArgs}, nil
	}
	defer func() { p.callExprParser = prevCallExprParser }()

	astBody, err := p.parseFnBodyStmts(ctx)
	if err != nil {
		return "", err
	}

	// Pure-instruction promotion (no return): if the function body is a
	// single instruction frame, promote it to fnDef.frame for the fast
	// direct-frame expansion path.
	if len(astBody) == 1 {
		if instrStmt, ok := astBody[0].(*InstructionStmt); ok {
			if promoted := tryPromoteInstruction(instrStmt.Frame, params, nil); promoted != nil {
				p.fns[nameTok.val] = &fnDef{params: params, frame: promoted}
				return nameTok.val, nil
			}
		}
	}

	// Post-parse analysis: determine return path from ReturnStmt nodes
	returns := collectReturnStmts(astBody)
	var rets []string

	if len(returns) == 0 {
		// No return: rets stays nil
	} else {
		// Check if single return at end of top-level body
		lastStmt := astBody[len(astBody)-1]
		_, lastIsReturn := lastStmt.(*ReturnStmt)
		singleTopLevel := len(returns) == 1 && lastIsReturn

		if singleTopLevel {
			ret := returns[0]
			// Return-instruction path: single return instruction at end
			if len(ret.Values) == 1 {
				if instrExpr, ok := ret.Values[0].(*InstructionExpr); ok {
					frame := instrExpr.Frame
					maxSlot := 0
					for _, v := range frame {
						if rs, ok := v.(returnSlot); ok {
							if int(rs) > maxSlot {
								maxSlot = int(rs)
							}
						}
					}
					modifiedFrame := maps.Clone(frame)
					for i := 1; i <= maxSlot; i++ {
						synthName := "@ret" + strconv.Itoa(i)
						rets = append(rets, synthName)
					}
					for k, v := range modifiedFrame {
						if rs, ok := v.(returnSlot); ok {
							modifiedFrame[k] = "@ret" + strconv.Itoa(int(rs))
						}
					}
					instrStmt := &InstructionStmt{Frame: modifiedFrame}
					astBody[len(astBody)-1] = instrStmt

					// Pure-instruction promotion
					if len(astBody) == 1 {
						if canPromote := tryPromoteInstruction(modifiedFrame, params, rets); canPromote != nil {
							p.fns[nameTok.val] = &fnDef{params: params, frame: canPromote}
							return nameTok.val, nil
						}
					}

					p.fns[nameTok.val] = &fnDef{params: params, rets: rets, astBody: astBody}
					return nameTok.val, nil
				}
			}

			// Zero-copy path: all values are IdentExpr
			allIdent := true
			for _, v := range ret.Values {
				if _, ok := v.(*IdentExpr); !ok {
					allIdent = false
					break
				}
			}
			if allIdent {
				for _, v := range ret.Values {
					rets = append(rets, v.(*IdentExpr).Name)
				}
				// Remove the ReturnStmt from the body
				astBody = astBody[:len(astBody)-1]
				p.fns[nameTok.val] = &fnDef{params: params, rets: rets, astBody: astBody}
				return nameTok.val, nil
			}
		}

		// Emit-and-jump path: multiple returns, returns in blocks,
		// or returns with literals/calls
		maxArity := 0
		for _, ret := range returns {
			a := returnStmtArity(ret, p.fns)
			if a > maxArity {
				maxArity = a
			}
		}
		for i := 1; i <= maxArity; i++ {
			rets = append(rets, "@ret"+strconv.Itoa(i))
		}
		// Leave ReturnStmt nodes in the body for emitFnBody to handle
	}

	p.fns[nameTok.val] = &fnDef{params: params, rets: rets, astBody: astBody}
	return nameTok.val, nil
}

// tryPromoteInstruction checks whether an instruction frame can be promoted
// to the fast fnDef.frame path. Returns the promoted frame, or nil.
func tryPromoteInstruction(frame map[string]any, params []paramDef, rets []string) map[string]any {
	opVal, _ := frame["op"].(string)
	for _, v := range frame {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s == opVal {
			continue
		}
		isParam := false
		for _, pd := range params {
			if pd.name == s {
				isParam = true
				break
			}
		}
		if isParam {
			continue
		}
		isRet := false
		for _, r := range rets {
			if r == s {
				isRet = true
				break
			}
		}
		if isRet {
			continue
		}
		return nil
	}
	promoted := maps.Clone(frame)
	for k, v := range promoted {
		if s, ok := v.(string); ok {
			for i, r := range rets {
				if s == r {
					promoted[k] = returnSlot(i + 1)
					break
				}
			}
		}
	}
	return promoted
}

// collectReturnStmts recursively collects all ReturnStmt nodes from an AST.
func collectReturnStmts(stmts []Stmt) []*ReturnStmt {
	var result []*ReturnStmt
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ReturnStmt:
			result = append(result, s)
		case *IfStmt:
			result = append(result, collectReturnStmts(s.Body)...)
			for _, elif := range s.ElseIfs {
				result = append(result, collectReturnStmts(elif.Body)...)
			}
			result = append(result, collectReturnStmts(s.Else)...)
		case *WhileStmt:
			result = append(result, collectReturnStmts(s.Body)...)
		case *LoopStmt:
			result = append(result, collectReturnStmts(s.Body)...)
		case *ForStmt:
			result = append(result, collectReturnStmts(s.Body)...)
		case *ModeBlockStmt:
			result = append(result, collectReturnStmts(s.Body)...)
		case *WaitStmt:
			result = append(result, collectReturnStmts(s.Body)...)
		}
	}
	return result
}

// returnStmtArity computes the return arity of a single ReturnStmt.
// ifExprArityStatic computes the max arity of an IfExpr using a fns map
// (for use outside a parser context, e.g., returnStmtArity).
func ifExprArityStatic(e *IfExpr, fns map[string]*fnDef) int {
	max := exprArityStatic(e.Tail, fns)
	for _, elif := range e.ElseIfs {
		if a := exprArityStatic(elif.Tail, fns); a > max {
			max = a
		}
	}
	if e.ElsTail != nil {
		if a := exprArityStatic(e.ElsTail, fns); a > max {
			max = a
		}
	}
	return max
}

// exprArityStatic computes arity using a fns map (no parser needed).
func exprArityStatic(expr Expr, fns map[string]*fnDef) int {
	switch e := expr.(type) {
	case *CallExpr:
		if fn, ok := fns[e.Name]; ok {
			return fn.returnCount()
		}
	case *IfExpr:
		return ifExprArityStatic(e, fns)
	case *ModeBlockExpr:
		return exprArityStatic(e.Tail, fns)
	}
	return 1
}

func returnStmtArity(ret *ReturnStmt, fns map[string]*fnDef) int {
	arity := 0
	for _, v := range ret.Values {
		switch e := v.(type) {
		case *CallExpr:
			if fn, ok := fns[e.Name]; ok {
				arity += fn.returnCount()
			} else {
				arity++
			}
		case *IfExpr:
			arity += ifExprArityStatic(e, fns)
		case *InstructionExpr:
			arity += frameReturnCount(e.Frame)
		default:
			arity++
		}
	}
	return arity
}

// parseFnBodyReturnItem parses a single item in a return statement.
// Supports the full expression language: arithmetic, comparisons, boolean
// chains, negation, type checks, function calls, constructors, &, mode
// block expressions, if-expressions, and parenthesized expressions.
func (p *parser) parseFnBodyReturnItem(ctx *fnBodyContext) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}

	// Mode block expression: return unlocked { get_self }
	if tok.kind == tokIdent && (tok.val == "locked" || tok.val == "unlocked") {
		return p.parseFnBodyModeBlockExpr(tok.val == "unlocked", ctx, "")
	}

	// If-expression: return if cond { a } else { b }
	if tok.kind == tokIdent && tok.val == "if" {
		return p.parseFnBodyIfExpr(ctx, "")
	}

	// Full expression: arithmetic, comparison, boolean, negation, type check,
	// constructors, function calls, parenthesized expressions.
	// parseBoolExpr handles all of these through parseArithPrimary (which
	// detects constructors, function calls, null/true/false, unary minus)
	// and parseBoolPrimary (which handles parenthesized and ! expressions).
	p.unget(tok)
	expr, err := p.parseBoolExpr(ctx.resolve)
	if err != nil {
		return nil, err
	}
	// Unwrap TruthyExpr for plain values (ident, number, constructor, fn call)
	if truthy, ok := expr.(*TruthyExpr); ok {
		inner := truthy.Value
		// Check for & operator after constructor
		ampPeek, err := p.next()
		if err != nil {
			return nil, err
		}
		if ampPeek.kind == tokAmpersand {
			if ctorExpr, ok := inner.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
				return nil, p.errorf(ampPeek.pos, "'&' cannot be used with Range (it would overwrite the step field)")
			}
			numExpr, err := p.parseFnBodyExpr()
			if err != nil {
				return nil, err
			}
			if err := p.checkFnBodyExprDeclared(numExpr, ctx, ampPeek.pos); err != nil {
				return nil, err
			}
			ctx.markExprUsed(numExpr)
			return &AmpersandExpr{Value: inner, Num: numExpr}, nil
		}
		p.unget(ampPeek)
		return inner, nil
	}
	return expr, nil
}

// parseFnBodyModeBlockExpr parses a locked/unlocked block used as an
// expression in a fn body context. The keyword has been consumed.
func (p *parser) parseFnBodyModeBlockExpr(unlock bool, ctx *fnBodyContext, comment string) (*ModeBlockExpr, error) {
	lbrace, err := p.expect(tokLBrace)
	if err != nil {
		return nil, err
	}
	stmts, err := p.parseFnBodyStmtsInner(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(lbrace.pos, "empty mode block expression")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(lbrace.pos, "last item in mode block expression must be a value-producing expression")
	}
	return &ModeBlockExpr{
		Unlock:  unlock,
		Body:    stmts[:len(stmts)-1],
		Tail:    tail.Expr,
		Comment: comment,
	}, nil
}

// parseFnBodyIfExprBranch parses a brace-delimited expression block
// (statements + tail expression) for an if-expression branch in a fn body.
func (p *parser) parseFnBodyIfExprBranch(ctx *fnBodyContext) ([]Stmt, Expr, error) {
	lbrace, err := p.expect(tokLBrace)
	if err != nil {
		return nil, nil, err
	}
	stmts, err := p.parseFnBodyStmtsInner(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	if len(stmts) == 0 {
		return nil, nil, p.errorf(lbrace.pos, "empty if-expression branch")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, nil, p.errorf(lbrace.pos, "last item in if-expression branch must be a value-producing expression")
	}
	return stmts[:len(stmts)-1], tail.Expr, nil
}

// parseFnBodyIfExpr parses an if-expression in a fn body context.
// The 'if' keyword has been consumed.
func (p *parser) parseFnBodyIfExpr(ctx *fnBodyContext, comment string) (*IfExpr, error) {
	// Parse condition using full boolean expression parser
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return nil, err
	}

	body, tail, err := p.parseFnBodyIfExprBranch(ctx)
	if err != nil {
		return nil, err
	}

	expr := &IfExpr{
		Cond:    cond,
		Body:    body,
		Tail:    tail,
		Comment: comment,
	}

	// Parse else-if / else chain (else is optional)
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind != tokIdent || tok.val != "else" {
			// No else clause — uncovered branches produce null
			p.unget(tok)
			return expr, nil
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			eiCond, err := p.parseBoolPrimary(ctx.resolve)
			if err != nil {
				return nil, err
			}
			eiCond, err = p.parseBoolChain(eiCond, ctx.resolve)
			if err != nil {
				return nil, err
			}
			eiBody, eiTail, err := p.parseFnBodyIfExprBranch(ctx)
			if err != nil {
				return nil, err
			}
			expr.ElseIfs = append(expr.ElseIfs, ElseIfExprClause{
				Cond: eiCond,
				Body: eiBody,
				Tail: eiTail,
			})
		} else {
			p.unget(peek)
			elsBody, elsTail, err := p.parseFnBodyIfExprBranch(ctx)
			if err != nil {
				return nil, err
			}
			expr.ElsBody = elsBody
			expr.ElsTail = elsTail
			return expr, nil
		}
	}
}

// parseFnBodyStmts parses fn body statements until '}'. The opening '{'
// has been consumed. Returns the parsed statements.
func (p *parser) parseFnBodyStmts(ctx *fnBodyContext) ([]Stmt, error) {
	return p.parseFnBodyStmtsInner(ctx, false)
}

// parseFnBodyStmtsInner parses fn body statements until '}'. If exprTail is
// true, the last item may be a bare expression (wrapped in exprTailStmt).
func (p *parser) parseFnBodyStmtsInner(ctx *fnBodyContext, exprTail bool) ([]Stmt, error) {
	savedInfo, savedDepth := ctx.pushFnScope()
	defer ctx.popFnScope(savedInfo, savedDepth)
	var astBody []Stmt
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent {
			if exprTail && tok.kind == tokNumber {
				num, _ := strconv.Atoi(tok.val)
				numExpr := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
				result, err := p.parseArithExprFromFull(numExpr, ctx.resolve)
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(tokRBrace); err != nil {
					return nil, err
				}
				astBody = append(astBody, &exprTailStmt{Expr: result})
				return astBody, nil
			}
			if exprTail && tok.kind == tokLParen {
				p.unget(tok)
				expr, err := p.parseBoolExpr(ctx.resolve)
				if err != nil {
					return nil, err
				}
				if truthy, ok := expr.(*TruthyExpr); ok {
					expr = truthy.Value
				}
				if _, err := p.expect(tokRBrace); err != nil {
					return nil, err
				}
				astBody = append(astBody, &exprTailStmt{Expr: expr})
				return astBody, nil
			}
			return nil, p.errorf(tok.pos, "expected statement or '}', got %s", tok.describe())
		}
		comment := p.docComment

		switch tok.val {
		case "locked", "unlocked":
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			body, err := p.parseFnBodyStmts(ctx)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, &ModeBlockStmt{
				Unlock:  tok.val == "unlocked",
				Body:    body,
				Comment: comment,
			})

		case "instruction":
			frame, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			if err := p.checkFnBodyInstructionDirections(frame, ctx.paramDirs, tok.pos); err != nil {
				return nil, err
			}
			astBody = append(astBody, &InstructionStmt{Frame: frame, Comment: comment})

		case "return":
			retPeek, err := p.next()
			if err != nil {
				return nil, err
			}
			if retPeek.kind == tokIdent && retPeek.val == "instruction" {
				frame, err := p.parseInstruction()
				if err != nil {
					return nil, err
				}
				if err := p.checkFnBodyInstructionDirections(frame, ctx.paramDirs, retPeek.pos); err != nil {
					return nil, err
				}
				astBody = append(astBody, &ReturnStmt{Values: []Expr{&InstructionExpr{Frame: frame}}, Comment: comment})
			} else {
				p.unget(retPeek)
				var values []Expr
				for {
					item, err := p.parseFnBodyReturnItem(ctx)
					if err != nil {
						return nil, err
					}
					values = append(values, item)
					sep, err := p.next()
					if err != nil {
						return nil, err
					}
					if sep.kind != tokComma {
						p.unget(sep)
						break
					}
				}
				astBody = append(astBody, &ReturnStmt{Values: values, Comment: comment})
			}

		case "let", "var":
			mutable := tok.val == "var"
			stmt, err := p.parseFnBodyLetVar(ctx, mutable, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt...)

		case "if":
			if exprTail {
				// Try as if-expression tail
				ifExpr, err := p.parseFnBodyIfExpr(ctx, comment)
				if err != nil {
					return nil, err
				}
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokRBrace {
					astBody = append(astBody, &exprTailStmt{Expr: ifExpr})
					return astBody, nil
				}
				return nil, p.errorf(peek.pos, "if-expression can only appear as the last item in an expression block")
			}
			stmt, err := p.parseFnBodyIfStmt(ctx, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "while":
			stmt, err := p.parseFnBodyWhileStmt(ctx, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "loop":
			stmt, err := p.parseFnBodyLoopStmt(ctx, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "for":
			stmt, err := p.parseFnBodyForStmt(ctx, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "wait":
			stmt, err := p.parseFnBodyWaitStmt(ctx, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "break":
			if p.loopDepth == 0 {
				return nil, p.errorf(tok.pos, "'break' outside of loop")
			}
			label := ""
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokIdent {
				if !p.loopLabels[peek.val] {
					return nil, p.errorf(peek.pos, "unknown loop label %q", peek.val)
				}
				label = peek.val
			} else {
				p.unget(peek)
			}
			astBody = append(astBody, &BreakStmt{Label: label, Comment: comment})

		case "fn", "private":
			return nil, p.errorf(tok.pos, "function definitions cannot be nested")

		case "behavior":
			return nil, p.errorf(tok.pos, "behavior definitions cannot be nested")

		case "else":
			return nil, p.errorf(tok.pos, "'else' without matching 'if'")

		case "continue":
			return nil, p.errorf(tok.pos, "'continue' is not supported; use labeled 'break' to exit a specific loop")

		default:
			// Check for labeled loop/while/for: `ident: loop { ... }` or `ident: while ...` or `ident: for ...`
			if !isConstructor(tok.val) && tok.val != "null" && tok.val != "true" && tok.val != "false" {
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokColon {
					peek2, err := p.next()
					if err != nil {
						return nil, err
					}
					if peek2.kind == tokIdent && (peek2.val == "loop" || peek2.val == "while" || peek2.val == "for") {
						label := tok.val
						if p.loopLabels[label] {
							return nil, p.errorf(tok.pos, "duplicate loop label %q", label)
						}
						switch peek2.val {
						case "loop":
							loopStmt, err := p.parseFnBodyLoopStmt(ctx, comment, label)
							if err != nil {
								return nil, err
							}
							astBody = append(astBody, loopStmt)
						case "while":
							whileStmt, err := p.parseFnBodyWhileStmt(ctx, comment, label)
							if err != nil {
								return nil, err
							}
							astBody = append(astBody, whileStmt)
						case "for":
							forStmt, err := p.parseFnBodyForStmt(ctx, comment, label)
							if err != nil {
								return nil, err
							}
							astBody = append(astBody, forStmt)
						}
						continue
					}
					p.unget(peek2)
				}
				p.unget(peek)
			}

			// Check for assignment, compound assignment, ++/--, or bare call
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokEquals || isCompoundAssignOp(peek.kind) || peek.kind == tokPlusPlus || peek.kind == tokMinusMinus {
				if err := p.checkVarName(tok.val, nil, tok.pos); err != nil {
					return nil, err
				}
			}
			if peek.kind == tokEquals {
				// Assignment: x = <expr>
				if err := ctx.canAssign(tok.val, p, tok.pos); err != nil {
					return nil, err
				}
				expr, err := p.parseFnBodyRHSExpr(ctx)
				if err != nil {
					return nil, err
				}
				astBody = append(astBody, &AssignStmt{Target: tok.val, Value: expr, Comment: comment, Pos: tok.pos})
			} else if isCompoundAssignOp(peek.kind) {
				// Compound assignment: x += <expr>
				if err := ctx.canCompound(tok.val, p, tok.pos); err != nil {
					return nil, err
				}
				rhs, err := p.parseBoolExpr(ctx.resolve)
				if err != nil {
					return nil, err
				}
				// Unwrap TruthyExpr for plain arithmetic/value results
				if truthy, ok := rhs.(*TruthyExpr); ok {
					rhs = truthy.Value
				}
				astBody = append(astBody, &CompoundAssignStmt{Target: tok.val, Op: peek.kind, Value: rhs, Comment: comment, Pos: tok.pos})
			} else if peek.kind == tokPlusPlus {
				if err := ctx.canCompound(tok.val, p, tok.pos); err != nil {
					return nil, err
				}
				astBody = append(astBody, &IncrDecrStmt{Target: tok.val, Op: tokPlusPlus, Comment: comment, Pos: tok.pos})
			} else if peek.kind == tokMinusMinus {
				if err := ctx.canCompound(tok.val, p, tok.pos); err != nil {
					return nil, err
				}
				astBody = append(astBody, &IncrDecrStmt{Target: tok.val, Op: tokMinusMinus, Comment: comment, Pos: tok.pos})
			} else {
				p.unget(peek)
				calleeName, callee, calleeErr := p.resolveFnName(tok)
				if calleeErr != nil {
					return nil, calleeErr
				}
				calleeTok := token{kind: tokIdent, val: calleeName, pos: tok.pos}

				if exprTail {
					// In exprTail mode, check for expr tail before treating as statement
					if isConstructor(tok.val) {
						ctor, err := p.parseFnBodyConstructorExpr(tok)
						if err != nil {
							return nil, err
						}
						// Check for & after constructor
						peek2, err := p.next()
						if err != nil {
							return nil, err
						}
						var tailExpr Expr = ctor
						if peek2.kind == tokAmpersand {
							if ctorExpr, ok := ctor.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
								return nil, p.errorf(peek2.pos, "'&' cannot be used with Range (it would overwrite the step field)")
							}
							numExpr, err := p.parseFnBodyExpr()
							if err != nil {
								return nil, err
							}
							tailExpr = &AmpersandExpr{Value: ctor, Num: numExpr}
						} else {
							p.unget(peek2)
						}
						if _, err := p.expect(tokRBrace); err != nil {
							return nil, err
						}
						astBody = append(astBody, &exprTailStmt{Expr: tailExpr})
						return astBody, nil
					}
					if tok.val == "null" || tok.val == "false" {
						if _, err := p.expect(tokRBrace); err != nil {
							return nil, err
						}
						astBody = append(astBody, &exprTailStmt{Expr: &LiteralExpr{Value: false}})
						return astBody, nil
					}
					if tok.val == "true" {
						if _, err := p.expect(tokRBrace); err != nil {
							return nil, err
						}
						astBody = append(astBody, &exprTailStmt{Expr: &LiteralExpr{Value: map[string]any{"num": 1}}})
						return astBody, nil
					}
					if callee != nil && callee.hasReturn() {
						args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, ctx)
						if err != nil {
							return nil, err
						}
						result := Expr(&CallExpr{Name: calleeName, Args: args, KwArgs: kwArgs})
						result, err = p.parseArithExprFromFull(result, ctx.resolve)
						if err != nil {
							return nil, err
						}
						final, handled, err := p.maybeExprContinuation(result, ctx.resolve)
						if err != nil {
							return nil, err
						}
						if handled {
							result = final
						}
						if _, err := p.expect(tokRBrace); err != nil {
							return nil, err
						}
						astBody = append(astBody, &exprTailStmt{Expr: result})
						return astBody, nil
					}
					if callee == nil {
						// Variable reference as tail
						resolved, err := ctx.resolve(tok)
						if err != nil {
							return nil, err
						}
						result, err := p.parseArithExprFromFull(resolved, ctx.resolve)
						if err != nil {
							return nil, err
						}
						final, handled, err := p.maybeExprContinuation(result, ctx.resolve)
						if err != nil {
							return nil, err
						}
						if handled {
							result = final
						}
						if _, err := p.expect(tokRBrace); err != nil {
							return nil, err
						}
						astBody = append(astBody, &exprTailStmt{Expr: result})
						return astBody, nil
					}
				}

				// Bare function call
				if callee == nil {
					return nil, p.errorf(tok.pos, "unknown function %q", tok.val)
				}
				args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, ctx)
				if err != nil {
					return nil, err
				}
				astBody = append(astBody, &CallStmt{
					Name:    calleeName,
					Args:    args,
					KwArgs:  kwArgs,
					Comment: comment,
				})
			}
		}
	}
	return astBody, nil
}

// parseFnBodyLetVar parses a let or var declaration in a fn body.
func (p *parser) parseFnBodyLetVar(ctx *fnBodyContext, mutable bool, comment string) ([]Stmt, error) {
	varTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	// Handle _ as first binding in multi-return
	firstDiscard := varTok.val == "_"
	if !firstDiscard {
		if err := p.checkVarName(varTok.val, nil, varTok.pos); err != nil {
			return nil, err
		}
	}
	if firstDiscard {
		// Peek for comma — if present, this is a multi-return with first discard
		sep, err := p.next()
		if err != nil {
			return nil, err
		}
		if sep.kind != tokComma {
			return nil, p.errorf(varTok.pos, "'_' cannot be used as a variable name")
		}
	}
	var sep token
	if !firstDiscard {
		sep, err = p.next()
		if err != nil {
			return nil, err
		}
	}
	if firstDiscard || sep.kind == tokComma {
		// Multi-return: let a, b, _ = fnCall args... OR instruction
		// Supports mixed modifiers: var a, let b, _ = ...
		var bindings []MultiBinding
		if firstDiscard {
			bindings = append(bindings, MultiBinding{Discard: true})
		} else {
			bindings = append(bindings, MultiBinding{Name: varTok.val, Mutable: mutable, Pos: varTok.pos})
		}
		// activeModifier: 0=let, 1=var
		activeModifier := 0
		if mutable {
			activeModifier = 1
		}
		for {
			bindTok, err := p.next()
			if err != nil {
				return nil, err
			}
			if bindTok.kind == tokEquals {
				break
			}
			if bindTok.kind != tokIdent {
				return nil, p.errorf(bindTok.pos, "expected identifier, '_', 'let', 'var', or '=' in binding list, got %s", bindTok.describe())
			}
			switch bindTok.val {
			case "_":
				bindings = append(bindings, MultiBinding{Discard: true})
			case "let":
				activeModifier = 0
				nameTok, err := p.expect(tokIdent)
				if err != nil {
					return nil, err
				}
				if err := p.checkVarName(nameTok.val, nil, nameTok.pos); err != nil {
					return nil, err
				}
				bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: false, Pos: nameTok.pos})
			case "var":
				activeModifier = 1
				nameTok, err := p.expect(tokIdent)
				if err != nil {
					return nil, err
				}
				if err := p.checkVarName(nameTok.val, nil, nameTok.pos); err != nil {
					return nil, err
				}
				bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: true, Pos: nameTok.pos})
			default:
				if err := p.checkVarName(bindTok.val, nil, bindTok.pos); err != nil {
					return nil, err
				}
				bindings = append(bindings, MultiBinding{
					Name:    bindTok.val,
					Mutable: activeModifier == 1,
					Pos:     bindTok.pos,
				})
			}
			next, err := p.next()
			if err != nil {
				return nil, err
			}
			if next.kind == tokEquals {
				break
			}
			if next.kind != tokComma {
				return nil, p.errorf(next.pos, "expected ',' or '=' in binding list, got %s", next.describe())
			}
		}
		// Parse the RHS: expression list
		firstTok, err := p.next()
		if err != nil {
			return nil, err
		}

		// Instruction is only valid as the sole RHS item
		if firstTok.kind == tokIdent && firstTok.val == "instruction" {
			frame, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			retCount := frameReturnCount(frame)
			if retCount == 0 {
				return nil, p.errorf(firstTok.pos, "instruction has no return slots (@N); cannot assign its result")
			}
			if len(bindings) > retCount {
				return nil, p.errorf(firstTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
			}
			if err := p.checkFnBodyInstructionDirections(frame, ctx.paramDirs, firstTok.pos); err != nil {
				return nil, err
			}
			for _, bind := range bindings {
				if !bind.Discard {
					ctx.declareFnVarWarn(bind.Name, bind.Mutable, p, bind.Pos)
				}
			}
			return []Stmt{&MultiReturnStmt{
				Bindings: bindings,
				Value:    &InstructionExpr{Frame: frame},
				Comment:  comment,
			}}, nil
		}

		// Parse expression list items
		p.unget(firstTok)
		var items []Expr
		bindingsConsumed := 0

		for bindingsConsumed < len(bindings) {
			if len(items) > 0 {
				comma, err := p.next()
				if err != nil {
					return nil, err
				}
				if comma.kind != tokComma {
					p.unget(comma)
					// If we have a single function call that doesn't fill all bindings,
					// give the specific "too many bindings" error.
					if len(items) == 1 {
						if ce, ok := items[0].(*CallExpr); ok {
							callee := p.fns[ce.Name]
							return nil, p.errorf(firstTok.pos, "too many bindings (%d) for function %q which returns %d values", len(bindings), ce.Name, callee.returnCount())
						}
					}
					return nil, p.errorf(comma.pos, "expected ',' between expression list items, got %s", comma.describe())
				}
			}

			tok, err := p.next()
			if err != nil {
				return nil, err
			}

			if tok.kind == tokIdent && (tok.val == "locked" || tok.val == "unlocked") {
				mbe, err := p.parseFnBodyModeBlockExpr(tok.val == "unlocked", ctx, comment)
				if err != nil {
					return nil, err
				}
				items = append(items, mbe)
				bindingsConsumed += p.exprArity(mbe.Tail)
				continue
			}

			if tok.kind == tokIdent && tok.val == "if" {
				ifExpr, err := p.parseFnBodyIfExpr(ctx, comment)
				if err != nil {
					return nil, err
				}
				items = append(items, ifExpr)
				bindingsConsumed += p.ifExprArity(ifExpr)
				continue
			}

			if tok.kind == tokIdent {
				name, callee, fnErr := p.resolveFnName(tok)
				if fnErr != nil {
					return nil, fnErr
				}
				if callee != nil {
					if !callee.hasReturn() {
						return nil, p.errorf(tok.pos, "function %q has no return value", name)
					}
					args, kwArgs, err := p.parseFnBodyCallArgs(callee, token{kind: tokIdent, val: name, pos: tok.pos}, ctx)
					if err != nil {
						return nil, err
					}
					items = append(items, &CallExpr{Name: name, Args: args, KwArgs: kwArgs})
					bindingsConsumed += callee.returnCount()
					continue
				}
			}

			// Simple expression (with arithmetic support)
			p.unget(tok)
			expr, err := p.parseFnBodyExpr()
			if err != nil {
				return nil, err
			}
			ctx.markExprUsed(expr)
			// Wrap with arithmetic parsing for numbers and identifiers
			if _, ok := expr.(*LiteralExpr); ok {
				if m, isMap := expr.(*LiteralExpr).Value.(map[string]any); isMap {
					if _, hasNum := m["num"]; hasNum && len(m) == 1 {
						arith, err := p.parseArithExprFromFull(expr, ctx.resolve)
						if err != nil {
							return nil, err
						}
						expr = arith
					}
				}
			} else if _, ok := expr.(*IdentExpr); ok {
				arith, err := p.parseArithExprFromFull(expr, ctx.resolve)
				if err != nil {
					return nil, err
				}
				expr = arith
			}
			items = append(items, expr)
			bindingsConsumed++
		}

		// Handle prefix matching on last item
		if bindingsConsumed > len(bindings) {
			if _, ok := items[len(items)-1].(*CallExpr); !ok {
				return nil, p.errorf(firstTok.pos, "too many values for %d bindings", len(bindings))
			}
			bindingsConsumed = len(bindings)
		}

		// Check no trailing expressions
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokComma {
			return nil, p.errorf(peek.pos, "too many expressions for %d bindings", len(bindings))
		}
		p.unget(peek)

		// Register variables
		for _, bind := range bindings {
			if !bind.Discard {
				ctx.declareFnVarWarn(bind.Name, bind.Mutable, p, bind.Pos)
			}
		}

		// If single item, use existing representation directly
		if len(items) == 1 {
			return []Stmt{&MultiReturnStmt{
				Bindings: bindings,
				Value:    items[0],
				Comment:  comment,
			}}, nil
		}

		// Multiple items: wrap in ExprListExpr
		return []Stmt{&MultiReturnStmt{
			Bindings: bindings,
			Value:    &ExprListExpr{Exprs: items},
			Comment:  comment,
		}}, nil
	}

	// Single: let/var varName = <expr>
	if sep.kind != tokEquals {
		return nil, p.errorf(sep.pos, "expected ',' or '=' after identifier, got %s", sep.describe())
	}

	expr, err := p.parseFnBodyRHSExpr(ctx)
	if err != nil {
		return nil, err
	}
	ctx.declareFnVarWarn(varTok.val, mutable, p, varTok.pos)
	return []Stmt{&LetStmt{
		Name:    varTok.val,
		Mutable: mutable,
		Value:   expr,
		Comment: comment,
	}}, nil
}

// parseFnBodyRHSExpr parses the RHS of a let/var/assignment in a fn body.
// Supports instruction, constructor, function call, and full expressions
// (arithmetic, comparison, boolean, type check).
func (p *parser) parseFnBodyRHSExpr(ctx *fnBodyContext) (Expr, error) {
	rhsTok, err := p.next()
	if err != nil {
		return nil, err
	}

	// Mode block expression RHS
	// Supports continuation: let x = unlocked { get_number v } + 1
	if rhsTok.kind == tokIdent && (rhsTok.val == "locked" || rhsTok.val == "unlocked") {
		mbe, err := p.parseFnBodyModeBlockExpr(rhsTok.val == "unlocked", ctx, "")
		if err != nil {
			return nil, err
		}
		result, err := p.parseArithExprFromFull(Expr(mbe), ctx.resolve)
		if err != nil {
			return nil, err
		}
		final, handled, err := p.maybeExprContinuation(result, ctx.resolve)
		if err != nil {
			return nil, err
		}
		if handled {
			return final, nil
		}
		return result, nil
	}

	// If-expression RHS
	// Supports continuation: let x = if cond { a } else { b } + 1
	if rhsTok.kind == tokIdent && rhsTok.val == "if" {
		ifExpr, err := p.parseFnBodyIfExpr(ctx, "")
		if err != nil {
			return nil, err
		}
		result, err := p.parseArithExprFromFull(Expr(ifExpr), ctx.resolve)
		if err != nil {
			return nil, err
		}
		final, handled, err := p.maybeExprContinuation(result, ctx.resolve)
		if err != nil {
			return nil, err
		}
		if handled {
			return final, nil
		}
		return result, nil
	}

	// Instruction RHS
	if rhsTok.kind == tokIdent && rhsTok.val == "instruction" {
		frame, err := p.parseInstruction()
		if err != nil {
			return nil, err
		}
		if err := p.checkFnBodyInstructionDirections(frame, ctx.paramDirs, rhsTok.pos); err != nil {
			return nil, err
		}
		if !frameHasReturnSlot(frame) {
			return nil, p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		return &InstructionExpr{Frame: frame}, nil
	}

	// Constructor RHS
	if rhsTok.kind == tokIdent && isConstructor(rhsTok.val) {
		ctor, err := p.parseFnBodyConstructorExpr(rhsTok)
		if err != nil {
			return nil, err
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokAmpersand {
			if ctorExpr, ok := ctor.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
				return nil, p.errorf(peek.pos, "'&' cannot be used with Range (it would overwrite the step field)")
			}
			numExpr, err := p.parseFnBodyExpr()
			if err != nil {
				return nil, err
			}
			return &AmpersandExpr{Value: ctor, Num: numExpr}, nil
		}
		p.unget(peek)
		return ctor, nil
	}

	// Function call RHS
	if rhsTok.kind == tokIdent {
		rhsName, callee, fnErr := p.resolveFnName(rhsTok)
		if fnErr != nil {
			return nil, fnErr
		}
		if callee != nil {
			if !callee.hasReturn() {
				return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsName)
			}
			args, kwArgs, err := p.parseFnBodyCallArgs(callee, token{kind: tokIdent, val: rhsName, pos: rhsTok.pos}, ctx)
			if err != nil {
				return nil, err
			}
			return &CallExpr{Name: rhsName, Args: args, KwArgs: kwArgs}, nil
		}
	}

	// Full expression (arithmetic, comparison, boolean, type check)
	// Put the token back and parse as a boolean expression (which subsumes
	// arithmetic, comparison, type check, and truthy).
	p.unget(rhsTok)
	expr, err := p.parseBoolExpr(ctx.resolve)
	if err != nil {
		return nil, err
	}
	// If the result is a bare truthy wrapper around a simple value, unwrap it
	// since the caller just wants the expression value, not a boolean check.
	if truthy, ok := expr.(*TruthyExpr); ok {
		// Check for & after variable/expression (not supported in declarations/assignments)
		ampPeek, err := p.next()
		if err != nil {
			return nil, err
		}
		if ampPeek.kind == tokAmpersand {
			return nil, p.errorf(ampPeek.pos, "'&' requires a type constructor on the left side; use set_number to attach a number to an existing value")
		}
		p.unget(ampPeek)
		return truthy.Value, nil
	}
	return expr, nil
}

// parseFnBodyIfStmt parses an if/else if/else statement in a fn body.
func (p *parser) parseFnBodyIfStmt(ctx *fnBodyContext, comment string) (*IfStmt, error) {
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return nil, err
	}
	// Allow boolean chain continuation
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseFnBodyStmts(ctx)
	if err != nil {
		return nil, err
	}

	stmt := &IfStmt{Cond: cond, Body: body, Comment: comment}

	// Parse optional else / else if
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokIdent && tok.val == "else" {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			if err := p.parseFnBodyElseIfChain(ctx, stmt); err != nil {
				return nil, err
			}
		} else {
			p.unget(peek)
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			elseBody, err := p.parseFnBodyStmts(ctx)
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		}
	} else {
		p.unget(tok)
	}

	return stmt, nil
}

// parseFnBodyElseIfChain parses the else if / else chain in a fn body.
func (p *parser) parseFnBodyElseIfChain(ctx *fnBodyContext, stmt *IfStmt) error {
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return err
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	body, err := p.parseFnBodyStmts(ctx)
	if err != nil {
		return err
	}
	stmt.ElseIfs = append(stmt.ElseIfs, ElseIfClause{Cond: cond, Body: body})

	tok, err := p.next()
	if err != nil {
		return err
	}
	if tok.kind == tokIdent && tok.val == "else" {
		peek, err := p.next()
		if err != nil {
			return err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			return p.parseFnBodyElseIfChain(ctx, stmt)
		}
		p.unget(peek)
		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		elseBody, err := p.parseFnBodyStmts(ctx)
		if err != nil {
			return err
		}
		stmt.Else = elseBody
	} else {
		p.unget(tok)
	}
	return nil
}

// parseFnBodyWhileStmt parses a while loop in a fn body.
func (p *parser) parseFnBodyWhileStmt(ctx *fnBodyContext, comment string, label ...string) (*WhileStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	p.enterLoop(lbl)
	body, err := p.parseFnBodyStmts(ctx)
	p.exitLoop(lbl)
	if err != nil {
		return nil, err
	}
	return &WhileStmt{Label: lbl, Cond: cond, Body: body, Comment: comment}, nil
}

// parseFnBodyLoopStmt parses a loop { ... } or loop N { ... } block in a fn body.
func (p *parser) parseFnBodyLoopStmt(ctx *fnBodyContext, comment string, label ...string) (*LoopStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}

	// Peek for count expression
	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	if peek.kind == tokLBrace {
		// Infinite loop: loop { ... }
		p.enterLoop(lbl)
		body, err := p.parseFnBodyStmts(ctx)
		p.exitLoop(lbl)
		if err != nil {
			return nil, err
		}
		return &LoopStmt{Label: lbl, Body: body, Comment: comment}, nil
	}

	// Counted loop: parse count expression
	count, err := p.parseSimpleExpr(peek, ctx.resolve, "'{' or count expression after 'loop'")
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	p.enterLoop(lbl)
	body, err := p.parseFnBodyStmts(ctx)
	p.exitLoop(lbl)
	if err != nil {
		return nil, err
	}
	return &LoopStmt{Label: lbl, Count: count, Body: body, Comment: comment}, nil
}

// parseFnBodyWaitStmt parses a wait statement in a fn body.
func (p *parser) parseFnBodyWaitStmt(ctx *fnBodyContext, comment string) (*WaitStmt, error) {
	// Parse ticks expression (same pattern as parseFnBodyLoopStmt count)
	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	ticks, err := p.parseSimpleExpr(peek, ctx.resolve, "ticks expression after 'wait'")
	if err != nil {
		return nil, err
	}

	// Check for optional condition block
	peek2, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek2.kind != tokLBrace {
		// Simple wait
		p.unget(peek2)
		return &WaitStmt{Ticks: ticks, Comment: comment}, nil
	}

	// Block wait: parse body + tail
	stmts, err := p.parseFnBodyStmtsInner(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(peek2.pos, "empty wait block")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(peek2.pos, "last item in wait block must be a value-producing expression")
	}
	return &WaitStmt{
		Ticks:   ticks,
		Body:    stmts[:len(stmts)-1],
		Tail:    tail.Expr,
		Comment: comment,
	}, nil
}

// parseFnBodyForStmt parses a for i in <range> { ... } loop in a fn body.
func (p *parser) parseFnBodyForStmt(ctx *fnBodyContext, comment string, label ...string) (*ForStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}

	iterTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if err := p.checkVarName(iterTok.val, nil, iterTok.pos); err != nil {
		return nil, err
	}

	inTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if inTok.val != "in" {
		return nil, p.errorf(inTok.pos, "expected 'in' after for variable, got %q", inTok.val)
	}

	rangeTok, err := p.next()
	if err != nil {
		return nil, err
	}
	var rangeExpr Expr
	if rangeTok.kind == tokIdent && rangeTok.val == "Range" {
		rangeExpr, err = p.parseFnBodyConstructorExpr(rangeTok)
		if err != nil {
			return nil, err
		}
	} else if rangeTok.kind == tokIdent {
		resolved, err := ctx.resolve(rangeTok)
		if err != nil {
			return nil, err
		}
		rangeExpr = resolved
	} else {
		return nil, p.errorf(rangeTok.pos, "expected Range constructor or variable after 'in', got %s", rangeTok.describe())
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	// Push scope for iter var + body
	savedInfo, savedDepth := ctx.pushFnScope()
	ctx.declareFnVarWarn(iterTok.val, false, p, iterTok.pos)

	p.enterLoop(lbl)
	body, err := p.parseFnBodyStmts(ctx)
	p.exitLoop(lbl)

	ctx.popFnScope(savedInfo, savedDepth)

	if err != nil {
		return nil, err
	}

	return &ForStmt{Label: lbl, IterVar: iterTok.val, Range: rangeExpr, Body: body, Comment: comment}, nil
}

// checkFnBodyInstructionDirections verifies that non-@N slots in an instruction
// frame within a fn body don't read from out-only parameters.
func (p *parser) checkFnBodyInstructionDirections(frame map[string]any, paramDirs map[string]string, pos int) error {
	for _, v := range frame {
		if _, ok := v.(returnSlot); ok {
			continue
		}
		if name, ok := v.(string); ok {
			if dir, ok := paramDirs[name]; ok && dir == "out" {
				return p.errorf(pos, "cannot read from output parameter %q in instruction input slot", name)
			}
		}
	}
	return nil
}

func (p *parser) skipBraceBlock() error {
	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	depth := 1
	for depth > 0 {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind == tokLBrace {
			depth++
		}
		if tok.kind == tokRBrace {
			depth--
		}
	}
	return nil
}

func (p *parser) skipFnDef() error {
	if _, err := p.expect(tokIdent); err != nil {
		return err
	}
	if _, err := p.expect(tokLParen); err != nil {
		return err
	}
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokRParen {
			break
		}
	}
	return p.skipBraceBlock()
}

func (p *parser) parseInstruction() (map[string]any, error) {
	opTok, err := p.expect(tokString)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	frame := map[string]any{"op": opTok.val}
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent && tok.kind != tokNumber {
			return nil, p.errorf(tok.pos, "expected field name or '}', got %s", tok.describe())
		}
		key := tok.val
		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}
		valTok, err := p.next()
		if err != nil {
			return nil, err
		}
		switch valTok.kind {
		case tokString, tokIdent:
			frame[key] = valTok.val
		case tokAt:
			numTok, err := p.expect(tokNumber)
			if err != nil {
				return nil, err
			}
			n, _ := strconv.Atoi(numTok.val)
			if n < 1 {
				return nil, p.errorf(numTok.pos, "@N return index must be >= 1, got @%d", n)
			}
			frame[key] = returnSlot(n)
		default:
			return nil, p.errorf(valTok.pos, "expected string, identifier, or @N, got %s", valTok.describe())
		}
	}

	// Validate that @N return slots form a contiguous sequence from @1.
	var maxSlot int
	slots := map[int]bool{}
	for _, v := range frame {
		if rs, ok := v.(returnSlot); ok {
			n := int(rs)
			slots[n] = true
			if n > maxSlot {
				maxSlot = n
			}
		}
	}
	for i := 1; i <= maxSlot; i++ {
		if !slots[i] {
			return nil, p.errorf(opTok.pos, "instruction %q has @%d but is missing @%d — return slots must be a contiguous sequence from @1", opTok.val, maxSlot, i)
		}
	}

	return frame, nil
}

func (p *parser) expandCall(name string, args []any, kwArgs map[string]any, retVals []any, b *frameBuilder, pos int, comment string, usedVars map[string]bool) error {
	fn := p.fns[name]
	if fn == nil {
		return p.errorf(pos, "unknown function %q", name)
	}

	paramMap := map[string]any{}
	posIdx := 0
	for _, pd := range fn.params {
		if pd.keyword == "" {
			paramMap[pd.name] = args[posIdx]
			posIdx++
		} else if kwArgs != nil {
			if val, ok := kwArgs[pd.keyword]; ok {
				paramMap[pd.name] = val
			}
		}
	}

	// Detect return/parameter name collisions. When a return name is also
	// a parameter name (e.g., `fn foo(x) { return x }`), we must not
	// overwrite the parameter mapping. Instead, track the collision and
	// emit a set_reg copy after body expansion.
	type retCopy struct{ from, to any }
	var retCopies []retCopy

	for i, retName := range fn.rets {
		target := any(false)
		if retVals != nil && i < len(retVals) {
			target = retVals[i]
		}
		if _, collision := paramMap[retName]; collision {
			retCopies = append(retCopies, retCopy{paramMap[retName], target})
		} else {
			paramMap[retName] = target
		}
	}

	if fn.frame != nil {
		instr := resolveInstructionFrame(fn.frame, retVals, paramMap, fn.keywordVarNames(), comment)
		b.emit(instr)
		return nil
	}

	// Temporarily merge the function's scope into p.fns so that transitive
	// dependencies (functions called by this fn but not explicitly imported
	// by the caller) are available during body expansion.
	var scopeAdded []string
	if fn.scope != nil {
		for k, v := range fn.scope {
			if _, exists := p.fns[k]; !exists {
				p.fns[k] = v
				scopeAdded = append(scopeAdded, k)
			}
		}
	}

	origPos := b.pos()
	err := p.emitFnBody(fn.astBody, b, paramMap, usedVars, comment, pos)

	// Remove temporarily added scope entries
	for _, k := range scopeAdded {
		delete(p.fns, k)
	}

	if err != nil {
		return err
	}
	for _, rc := range retCopies {
		f := map[string]any{"op": "set_reg", "1": rc.from, "2": rc.to}
		setComment(f, comment)
		b.emit(f)
	}
	// Patch @return placeholders to jump past the entire function expansion
	afterAll := b.pos()
	for j := origPos; j < afterAll; j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@return" {
			b.frames[j] = map[string]any{
				"op":   "set_reg",
				"1":    false,
				"2":    false,
				"next": frameRef(afterAll),
			}
		}
	}
	return nil
}
