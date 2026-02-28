package compiler

import (
	"fmt"
	"io/fs"
	"maps"
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
		if _, ok := ctx.fnVars[e.Name]; ok {
			return nil
		}
		return p.errorf(pos, "undeclared variable %q", e.Name)
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
		} else if _, ok := ctx.fnVars[tok.val]; !ok {
			return nil, p.errorf(tok.pos, "undeclared variable %q", tok.val)
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
	if tok.kind == tokIdent && p.callExprParser != nil && p.fns[tok.val] != nil && p.fns[tok.val].hasReturn() {
		callExpr, err := p.callExprParser(p.fns[tok.val], tok)
		if err != nil {
			return nil, err
		}
		return p.parseArithExprFromFull(callExpr, ctx.resolve)
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
	if _, err := p.expect(tokLParen); err != nil {
		return nil, p.errorf(nameTok.pos, "expected '(' after %s", nameTok.val)
	}
	switch nameTok.val {
	case "Item", "Component", "Technology", "Value":
		argTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if argTok.kind != tokString {
			return nil, p.errorf(argTok.pos, "expected string argument, got %s", argTok.describe())
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &ConstructorExpr{
			TypeName: nameTok.val,
			Args:     []Expr{&LiteralExpr{Value: argTok.val}},
		}, nil
	case "Coordinate":
		x, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokComma); err != nil {
			return nil, err
		}
		y, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &ConstructorExpr{
			TypeName: "Coordinate",
			Args:     []Expr{x, y},
		}, nil
	case "Range":
		arg1, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			return &ConstructorExpr{
				TypeName: "Range",
				Args: []Expr{
					&LiteralExpr{Value: map[string]any{"num": 0}},
					arg1,
					&LiteralExpr{Value: map[string]any{"num": 1}},
				},
			}, nil
		}
		if peek.kind != tokComma {
			return nil, p.errorf(peek.pos, "expected ',' or ')' after Range argument, got %s", peek.describe())
		}
		arg2, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		peek, err = p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			return &ConstructorExpr{
				TypeName: "Range",
				Args:     []Expr{arg1, arg2, &LiteralExpr{Value: map[string]any{"num": 1}}},
			}, nil
		}
		if peek.kind != tokComma {
			return nil, p.errorf(peek.pos, "expected ',' or ')' after Range argument, got %s", peek.describe())
		}
		arg3, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		if lit, ok := arg3.(*LiteralExpr); ok {
			if m, ok := lit.Value.(map[string]any); ok {
				if n, ok := m["num"]; ok && n == 0 {
					return nil, p.errorf(nameTok.pos, "Range step cannot be zero")
				}
			}
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &ConstructorExpr{
			TypeName: "Range",
			Args:     []Expr{arg1, arg2, arg3},
		}, nil
	}
	return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
}

// parseFnBodyCallArgs parses positional and keyword arguments for a
// function call in a fn body, returning AST-typed expressions.
// Supports both unparenthesized and parenthesized call syntax.
func (p *parser) parseFnBodyCallArgs(callee *fnDef, calleeTok token, ctx *fnBodyContext) ([]Expr, map[string]Expr, error) {
	paramDirs := ctx.paramDirs
	letVars := ctx.fnVars
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
func fnBodyExprDir(expr Expr, paramDirs map[string]string, fnVars map[string]bool) string {
	if e, ok := expr.(*IdentExpr); ok {
		if dir, ok := paramDirs[e.Name]; ok {
			return dir
		}
		if mutable, declared := fnVars[e.Name]; declared {
			if mutable {
				return "inout"
			}
			return "in"
		}
		return "inout"
	}
	return "in" // literals, constructors, etc.
}

// checkFnBodyCallDirectionsExpr checks direction compatibility for AST-typed args.
func (p *parser) checkFnBodyCallDirectionsExpr(callee *fnDef, calleeName string, args []Expr, kwArgs map[string]Expr, paramDirs map[string]string, letVars map[string]bool, pos int) error {
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
// Mirrors emitBhvArithTo but resolves operands through paramMap.
func (p *parser) emitFnArithTo(expr *ArithExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	ac := &arithCounter{}
	_, err := p.emitFnArithNode(expr, target, b, paramMap, usedVars, comment, pos, ac)
	return err
}

func (p *parser) emitFnArithNode(expr *ArithExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int, ac *arithCounter) (any, error) {
	var lhs any
	if sub, ok := expr.LHS.(*ArithExpr); ok {
		tmp := ac.next(usedVars)
		val, err := p.emitFnArithNode(sub, tmp, b, paramMap, usedVars, "", pos, ac)
		if err != nil {
			return nil, err
		}
		lhs = val
	} else {
		val, err := p.emitExprGetValue(expr.LHS, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, err
		}
		lhs = val
	}

	var rhs any
	if sub, ok := expr.RHS.(*ArithExpr); ok {
		tmp := ac.next(usedVars)
		val, err := p.emitFnArithNode(sub, tmp, b, paramMap, usedVars, "", pos, ac)
		if err != nil {
			return nil, err
		}
		rhs = val
	} else {
		val, err := p.emitExprGetValue(expr.RHS, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, err
		}
		rhs = val
	}

	f := map[string]any{
		"op": arithmeticOpName(expr.Op),
		"1":  lhs,
		"2":  rhs,
		"3":  target,
	}
	setComment(f, comment)
	b.emit(f)
	return target, nil
}

// emitFnBoolExprTo emits a boolean expression (comparison/typecheck/truthy/chain)
// writing the result to target. Mirrors emitBhvBoolExprTo but resolves operands
// through paramMap.
func (p *parser) emitFnBoolExprTo(expr Expr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	resolved, err := p.resolveFnBoolTree(expr, b, paramMap, usedVars, pos)
	if err != nil {
		return err
	}

	// Single non-negated leaf: delegate to specialized emitters (matching old behavior).
	// Negated leaves fall through to the chain/group path which handles
	// negation via emitBoolCheckFrame's target swap.
	if resolved.isLeaf() && !resolved.term.negated {
		t := resolved.term
		switch {
		case isTypeCheckOp(t.op):
			p.emitTypeCheck(t.lhs, target, t.rhs.(string), b, comment)
		case t.op == tokTruthy:
			p.emitTruthyCheck(t.lhs, target, b, comment)
		default:
			p.emitComparison(t.op, t.lhs, t.rhs, target, b, comment)
		}
		return nil
	}

	// Chain/group: use recursive emitter
	totalChecks := resolved.frameCount()
	base := b.pos()
	falsePos := base + totalChecks
	truePos := base + totalChecks + 1
	afterPos := base + totalChecks + 2

	p.emitResolvedBoolFrames(resolved, frameRef(truePos), frameRef(falsePos), b, comment)

	// False frame
	b.emit(map[string]any{
		"op":   "set_reg",
		"1":    false,
		"2":    target,
		"next": frameRef(afterPos),
	})
	// True frame
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  map[string]any{"num": 1},
		"2":  target,
	})
	return nil
}

// resolveFnBoolTree walks an Expr tree, resolving operands through paramMap
// and emitting arithmetic frames. Produces a resolvedBoolExpr tree.
func (p *parser) resolveFnBoolTree(expr Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, pos int) (*resolvedBoolExpr, error) {
	switch e := expr.(type) {
	case *CompareExpr:
		lhs, err := p.emitExprGetValue(e.LHS, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, err
		}
		rhs, err := p.emitExprGetValue(e.RHS, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: e.Op, lhs: lhs, rhs: rhs}}, nil
	case *TypeCheckExpr:
		lhs, err := p.emitExprGetValue(e.Value, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokIs, lhs: lhs, rhs: e.TypeSlot}}, nil
	case *TruthyExpr:
		lhs, err := p.emitExprGetValue(e.Value, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokTruthy, lhs: lhs}}, nil
	case *NotExpr:
		resolved, err := p.resolveFnBoolTree(e.Value, b, paramMap, usedVars, pos)
		if err != nil {
			return nil, err
		}
		negateResolved(resolved)
		return resolved, nil
	case *BoolChainExpr:
		children := make([]*resolvedBoolExpr, len(e.Children))
		for i, child := range e.Children {
			resolved, err := p.resolveFnBoolTree(child, b, paramMap, usedVars, pos)
			if err != nil {
				return nil, err
			}
			children[i] = resolved
		}
		return &resolvedBoolExpr{chainOp: e.Op, children: children}, nil
	default:
		return nil, fmt.Errorf("unsupported boolean expression type %T", expr)
	}
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
					if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, comment, usedVars); err != nil {
						return err
					}
					retOffset += rc
				case *InstructionExpr:
					rc := frameReturnCount(e.Frame)
					retVals := make([]any, rc)
					for j := 0; j < rc; j++ {
						retVals[j] = resolveVarName("@ret"+strconv.Itoa(retOffset+j+1), paramMap)
					}
					resolved := resolveInstructionFrame(e.Frame, retVals, paramMap, nil, comment)
					b.emit(resolved)
					retOffset += rc
				default:
					target := resolveVarName("@ret"+strconv.Itoa(retOffset+1), paramMap)
					if err := p.emitExprTo(val, target, b, paramMap, usedVars, comment, pos); err != nil {
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

		// Patch false branch of all check frames in this condition to here
		falseTarget := frameRef(b.pos())
		for j := checkStart; j < checkStart+checkCount; j++ {
			f := b.get(j)
			for k, v := range f {
				if ref, ok := v.(frameRef); ok && ref == falsePlaceholder {
					f[k] = falseTarget
				}
			}
		}
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

		// Patch false branches
		falseTarget := frameRef(b.pos())
		for j := checkStart; j < checkStart+checkCount; j++ {
			f := b.get(j)
			for k, v := range f {
				if ref, ok := v.(frameRef); ok && ref == falsePlaceholder {
					f[k] = falseTarget
				}
			}
		}
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

		falseTarget := frameRef(b.pos())
		for j := checkStart; j < checkStart+checkCount; j++ {
			f := b.get(j)
			for k, v := range f {
				if ref, ok := v.(frameRef); ok && ref == falsePlaceholder {
					f[k] = falseTarget
				}
			}
		}
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

	// Jump back to loop start
	lastFrame := b.get(b.pos() - 1)
	if _, hasNext := lastFrame["next"]; !hasNext {
		lastFrame["next"] = frameRef(loopStart)
	} else {
		// Emit explicit jump back
		b.emit(map[string]any{
			"op":   "set_reg",
			"1":    false,
			"2":    false,
			"next": frameRef(loopStart),
		})
	}

	// Patch false branches to after the loop
	afterLoop := frameRef(b.pos())
	for j := checkStart; j < checkStart+checkCount; j++ {
		f := b.get(j)
		for k, v := range f {
			if ref, ok := v.(frameRef); ok && ref == falsePlaceholder {
				f[k] = afterLoop
			}
		}
	}

	// Patch @break placeholders
	for j := origLen; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			fLabel, _ := f["label"].(string)
			if fLabel == "" || fLabel == s.Label {
				b.frames[j] = map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": afterLoop,
				}
			}
		}
	}

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

	// Jump back to loop start
	if b.pos() > loopStart {
		lastFrame := b.get(b.pos() - 1)
		if op, _ := lastFrame["op"].(string); op != "@break" {
			if _, hasNext := lastFrame["next"]; !hasNext {
				lastFrame["next"] = frameRef(loopStart)
			} else {
				b.emit(map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": frameRef(loopStart),
				})
			}
		}
	}

	// Patch break frames to point after the loop
	afterLoop := frameRef(b.pos())
	for j := origLen; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			fLabel, _ := f["label"].(string)
			if fLabel == "" || fLabel == s.Label {
				b.frames[j] = map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": afterLoop,
				}
			}
		}
	}

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
		"1":  map[string]any{"num": 0},
		"2":  counterVar,
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
	if b.pos()-1 > origLen-1 {
		lastBodyFrame := b.get(incrFrame - 1)
		if op, _ := lastBodyFrame["op"].(string); op != "@break" {
			if _, hasNext := lastBodyFrame["next"]; !hasNext {
				lastBodyFrame["next"] = frameRef(incrFrame)
			}
		}
	}

	// Patch CHECK exits: larger and equal → afterLoop
	afterLoop := frameRef(b.pos())
	check := b.get(checkFrame)
	check[checkLarger] = afterLoop
	check["next"] = afterLoop

	// Patch @break placeholders
	for j := origLen; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			fLabel, _ := f["label"].(string)
			if fLabel == "" || fLabel == s.Label {
				b.frames[j] = map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": afterLoop,
				}
			}
		}
	}

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
	if b.pos()-1 > origLen-1 {
		lastBodyFrame := b.get(incrFrame - 1)
		if op, _ := lastBodyFrame["op"].(string); op != "@break" {
			if _, hasNext := lastBodyFrame["next"]; !hasNext {
				lastBodyFrame["next"] = frameRef(incrFrame)
			}
		}
	}

	afterLoop := frameRef(b.pos())
	if stepSign > 0 {
		check[checkLarger] = afterLoop
		check["next"] = afterLoop
	} else {
		check[checkSmaller] = afterLoop
		check["next"] = afterLoop
	}

	for j := origLen; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			fLabel, _ := f["label"].(string)
			if fLabel == "" || fLabel == s.Label {
				b.frames[j] = map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": afterLoop,
				}
			}
		}
	}

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

	if b.pos()-1 > origLen-1 {
		lastBodyFrame := b.get(incrFrame - 1)
		if op, _ := lastBodyFrame["op"].(string); op != "@break" {
			if _, hasNext := lastBodyFrame["next"]; !hasNext {
				lastBodyFrame["next"] = frameRef(incrFrame)
			}
		}
	}

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

	for j := origLen; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			fLabel, _ := f["label"].(string)
			if fLabel == "" || fLabel == s.Label {
				b.frames[j] = map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": afterLoop,
				}
			}
		}
	}

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
		if err := p.parseUserFn(); err != nil {
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
			if fnTok.val != "fn" {
				return nil, p.errorf(fnTok.pos, "expected 'fn' after 'private', got %q", fnTok.val)
			}
			if err := p.skipFnDef(); err != nil {
				return nil, err
			}
		case "fn":
			if err := p.skipFnDef(); err != nil {
				return nil, err
			}
		default:
			return nil, p.errorf(tok.pos, "expected 'behavior', 'fn', or 'private', got %q", tok.val)
		}
	}
}

func (p *parser) collectUserFns() error {
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return nil
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
			if fnTok.val != "fn" {
				return p.errorf(fnTok.pos, "expected 'fn' after 'private', got %q", fnTok.val)
			}
			if err := p.parseUserFn(); err != nil {
				return err
			}
		case "fn":
			if err := p.parseUserFn(); err != nil {
				return err
			}
		default:
			return p.errorf(tok.pos, "expected 'behavior', 'fn', or 'private', got %q", tok.val)
		}
	}
}

// fnBodyContext holds the shared state for fn body parsing.
type fnVarInfo struct {
	mutable bool
	depth   int
	used    bool
}

type fnBodyContext struct {
	paramDirs    map[string]string    // param name -> effective direction
	fnVars       map[string]bool      // name -> true=mutable (var), false=immutable (let)
	fnVarInfo    map[string]fnVarInfo // detailed var info for shadowing warnings
	fnScopeDepth int                  // current nesting depth (0 = fn top-level)
	resolve      operandResolver
}

// pushFnScope saves the current fnVars and fnVarInfo maps and increments scope depth.
func (ctx *fnBodyContext) pushFnScope() (map[string]bool, map[string]fnVarInfo, int) {
	savedVars := make(map[string]bool, len(ctx.fnVars))
	for k, v := range ctx.fnVars {
		savedVars[k] = v
	}
	savedInfo := make(map[string]fnVarInfo, len(ctx.fnVarInfo))
	for k, v := range ctx.fnVarInfo {
		savedInfo[k] = v
	}
	depth := ctx.fnScopeDepth
	ctx.fnScopeDepth++
	return savedVars, savedInfo, depth
}

// popFnScope restores fnVars and fnVarInfo from saved copies and decrements scope depth.
func (ctx *fnBodyContext) popFnScope(savedVars map[string]bool, savedInfo map[string]fnVarInfo, depth int) {
	ctx.fnVars = savedVars
	ctx.fnVarInfo = savedInfo
	ctx.fnScopeDepth = depth
}

// declareFnVar registers a variable at the current fn scope depth.
func (ctx *fnBodyContext) declareFnVar(name string, mutable bool) {
	ctx.fnVars[name] = mutable
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
	if mutable, ok := ctx.fnVars[name]; ok {
		if !mutable {
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

func (p *parser) parseUserFn() error {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}

	params, err := p.parseParamList()
	if err != nil {
		return err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}

	// Build direction maps for enforcement in fn body
	paramDirs := map[string]string{} // param name -> effective direction
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
	}
	ctx := &fnBodyContext{
		paramDirs: paramDirs,
		fnVars:    map[string]bool{},
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
		return err
	}

	// Pure-instruction promotion (no return): if the function body is a
	// single instruction frame, promote it to fnDef.frame for the fast
	// direct-frame expansion path.
	if len(astBody) == 1 {
		if instrStmt, ok := astBody[0].(*InstructionStmt); ok {
			if promoted := tryPromoteInstruction(instrStmt.Frame, params, nil); promoted != nil {
				p.fns[nameTok.val] = &fnDef{params: params, frame: promoted}
				return nil
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
							return nil
						}
					}

					p.fns[nameTok.val] = &fnDef{params: params, rets: rets, astBody: astBody}
					return nil
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
				return nil
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
	return nil
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
// Handles function calls (with return values), identifiers, numbers,
// null, constructors, &, and $register references.
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

	// Parenthesized expression: return (a > 5)
	if tok.kind == tokLParen {
		inner, err := p.parseBoolExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		if truthy, ok := inner.(*TruthyExpr); ok {
			return truthy.Value, nil
		}
		return inner, nil
	}

	// Function call: known function with a return value
	if tok.kind == tokIdent && !isConstructor(tok.val) && tok.val != "null" && tok.val != "true" && tok.val != "false" && !strings.HasPrefix(tok.val, "$") {
		callee := p.fns[tok.val]
		if callee != nil && callee.hasReturn() {
			args, kwArgs, err := p.parseFnBodyCallArgs(callee, tok, ctx)
			if err != nil {
				return nil, err
			}
			return &CallExpr{Name: tok.val, Args: args, KwArgs: kwArgs}, nil
		}
	}

	// Otherwise, parse as a simple expression
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

// parseFnBodyModeBlockExpr parses a locked/unlocked block used as an
// expression in a fn body context. The keyword has been consumed.
func (p *parser) parseFnBodyModeBlockExpr(unlock bool, ctx *fnBodyContext, comment string) (*ModeBlockExpr, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	stmts, err := p.parseFnBodyStmtsInner(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(0, "empty mode block expression")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(0, "last item in mode block expression must be a value-producing expression")
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
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, nil, err
	}
	stmts, err := p.parseFnBodyStmtsInner(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	if len(stmts) == 0 {
		return nil, nil, p.errorf(0, "empty if-expression branch")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, nil, p.errorf(0, "last item in if-expression branch must be a value-producing expression")
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
	savedVars, savedInfo, savedDepth := ctx.pushFnScope()
	defer ctx.popFnScope(savedVars, savedInfo, savedDepth)
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
				astBody = append(astBody, &ReturnStmt{Values: []Expr{&InstructionExpr{Frame: frame}}})
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
				astBody = append(astBody, &ReturnStmt{Values: values})
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
				callee := p.fns[tok.val]

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
						args, kwArgs, err := p.parseFnBodyCallArgs(callee, tok, ctx)
						if err != nil {
							return nil, err
						}
						result := Expr(&CallExpr{Name: tok.val, Args: args, KwArgs: kwArgs})
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
				args, kwArgs, err := p.parseFnBodyCallArgs(callee, tok, ctx)
				if err != nil {
					return nil, err
				}
				astBody = append(astBody, &CallStmt{
					Name:    tok.val,
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
				if callee := p.fns[tok.val]; callee != nil {
					if !callee.hasReturn() {
						return nil, p.errorf(tok.pos, "function %q has no return value", tok.val)
					}
					args, kwArgs, err := p.parseFnBodyCallArgs(callee, tok, ctx)
					if err != nil {
						return nil, err
					}
					items = append(items, &CallExpr{Name: tok.val, Args: args, KwArgs: kwArgs})
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
		callee := p.fns[rhsTok.val]
		if callee != nil {
			if !callee.hasReturn() {
				return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
			}
			args, kwArgs, err := p.parseFnBodyCallArgs(callee, rhsTok, ctx)
			if err != nil {
				return nil, err
			}
			return &CallExpr{Name: rhsTok.val, Args: args, KwArgs: kwArgs}, nil
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
	var count Expr
	switch peek.kind {
	case tokNumber:
		num, _ := strconv.Atoi(peek.val)
		count = &LiteralExpr{Value: map[string]any{"num": num}}
		count, err = p.parseArithExprFromFull(count, ctx.resolve)
		if err != nil {
			return nil, err
		}
	case tokLParen:
		count, err = p.parseArithExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	case tokIdent:
		resolved, err := ctx.resolve(peek)
		if err != nil {
			return nil, err
		}
		count, err = p.parseArithExprFromFull(resolved, ctx.resolve)
		if err != nil {
			return nil, err
		}
	default:
		return nil, p.errorf(peek.pos, "expected '{' or count expression after 'loop', got %s", peek.describe())
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

	var ticks Expr
	switch peek.kind {
	case tokNumber:
		num, _ := strconv.Atoi(peek.val)
		ticks = &LiteralExpr{Value: map[string]any{"num": num}}
		ticks, err = p.parseArithExprFromFull(ticks, ctx.resolve)
		if err != nil {
			return nil, err
		}
	case tokLParen:
		ticks, err = p.parseArithExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	case tokIdent:
		resolved, err := ctx.resolve(peek)
		if err != nil {
			return nil, err
		}
		ticks, err = p.parseArithExprFromFull(resolved, ctx.resolve)
		if err != nil {
			return nil, err
		}
	default:
		return nil, p.errorf(peek.pos, "expected ticks expression after 'wait', got %s", peek.describe())
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
	savedVars, savedInfo, savedDepth := ctx.pushFnScope()
	ctx.declareFnVarWarn(iterTok.val, false, p, iterTok.pos)

	p.enterLoop(lbl)
	body, err := p.parseFnBodyStmts(ctx)
	p.exitLoop(lbl)

	ctx.popFnScope(savedVars, savedInfo, savedDepth)

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

	origPos := b.pos()
	if err := p.emitFnBody(fn.astBody, b, paramMap, usedVars, comment, pos); err != nil {
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
