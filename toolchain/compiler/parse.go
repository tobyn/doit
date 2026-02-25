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
		} else if tok.val == "null" {
			base = &LiteralExpr{Value: false}
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
		numExpr, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, err
		}
		return &AmpersandExpr{Value: base, Num: numExpr}, nil
	}
	p.unget(peek)
	return base, nil
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
	}
	return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
}

// parseFnBodyCallArgs parses positional and keyword arguments for a
// function call in a fn body, returning AST-typed expressions.
func (p *parser) parseFnBodyCallArgs(callee *fnDef, calleeTok token, paramDirs map[string]string, letVars map[string]bool) ([]Expr, map[string]Expr, error) {
	posCount := callee.positionalCount()
	args := make([]Expr, posCount)
	for i := 0; i < posCount; i++ {
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

		arg, err := p.parseFnBodyExpr()
		if err != nil {
			return nil, nil, err
		}
		args[i] = arg
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if (peek.kind == tokString || peek.kind == tokIdent) && callee.positionalCount() < len(callee.params) {
		if peek.kind == tokString {
			return nil, nil, p.errorf(peek.pos,
				"too many positional arguments for %s (remaining parameters are keyword-only)", calleeTok.val)
		}
		p.unget(peek)
	} else if peek.kind == tokComma {
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
			val, err := p.parseFnBodyExpr()
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

	if err := p.checkFnBodyCallDirectionsExpr(callee, calleeTok.val, args, kwArgs, paramDirs, letVars, calleeTok.pos); err != nil {
		return nil, nil, err
	}

	return args, kwArgs, nil
}

// fnBodyExprDir determines the effective direction of an AST expression
// in a fn body context.
func fnBodyExprDir(expr Expr, paramDirs map[string]string, letVars map[string]bool) string {
	if e, ok := expr.(*IdentExpr); ok {
		if dir, ok := paramDirs[e.Name]; ok {
			return dir
		}
		if letVars[e.Name] {
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
		case *MultiReturnStmt:
			for _, bind := range s.Bindings {
				if !bind.Discard {
					if _, mapped := paramMap[bind.Name]; !mapped {
						paramMap[bind.Name] = allocUniqueVar(bind.Name, usedVars)
					}
				}
			}
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
	}
	return fmt.Errorf("unsupported expression type %T in emitExprTo", expr)
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

	// Runtime path — only Coordinate can be runtime (others always have literal args)
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

		case *LockStmt:
			op := "lock"
			if s.Unlock {
				op = "unlock"
			}
			callComment := s.Comment
			if callComment == "" {
				callComment = comment
			}
			f := map[string]any{"op": op}
			setComment(f, callComment)
			b.emit(f)

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
			case *InstructionExpr:
				resolved := resolveInstructionFrame(v.Frame, retVals, paramMap, nil, callComment)
				b.emit(resolved)
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
	letVars := map[string]bool{} // tracks let-declared locals in fn body

	var astBody []Stmt
	var rets []string
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent {
			return p.errorf(tok.pos, "expected function call or '}', got %s", tok.describe())
		}
		comment := p.docComment

		// Handle lock/unlock in fn body
		if tok.val == "lock" || tok.val == "unlock" {
			astBody = append(astBody, &LockStmt{
				Unlock:  tok.val == "unlock",
				Comment: comment,
			})
			continue
		}

		// Handle bare instruction statement in fn body
		if tok.val == "instruction" {
			frame, err := p.parseInstruction()
			if err != nil {
				return err
			}
			if err := p.checkFnBodyInstructionDirections(frame, paramDirs, tok.pos); err != nil {
				return err
			}
			astBody = append(astBody, &InstructionStmt{Frame: frame, Comment: comment})
			continue
		}

		// Handle return statement: return instruction OR return item (',' item)*
		if tok.val == "return" {
			retPeek, err := p.next()
			if err != nil {
				return err
			}
			if retPeek.kind == tokIdent && retPeek.val == "instruction" {
				frame, err := p.parseInstruction()
				if err != nil {
					return err
				}
				if err := p.checkFnBodyInstructionDirections(frame, paramDirs, retPeek.pos); err != nil {
					return err
				}
				// Extract @N return slots and create synthetic ret names
				maxSlot := 0
				for _, v := range frame {
					if rs, ok := v.(returnSlot); ok {
						if int(rs) > maxSlot {
							maxSlot = int(rs)
						}
					}
				}
				rets = nil
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
				astBody = append(astBody, &InstructionStmt{Frame: modifiedFrame, Comment: comment})
				continue
			}
			p.unget(retPeek)

			// return item (',' item)* — identifiers, numbers, or null
			rets = nil
			retIdx := 0
			for {
				retTok, err := p.next()
				if err != nil {
					return err
				}
				switch retTok.kind {
				case tokIdent:
					if retTok.val == "null" {
						retIdx++
						synthName := "@ret" + strconv.Itoa(retIdx)
						astBody = append(astBody, &LetStmt{
							Name: synthName,
							Value: &CallExpr{
								Name: "set_reg",
								Args: []Expr{&LiteralExpr{Value: false}},
							},
						})
						rets = append(rets, synthName)
					} else {
						rets = append(rets, retTok.val)
					}
				case tokNumber:
					retIdx++
					synthName := "@ret" + strconv.Itoa(retIdx)
					num, _ := strconv.Atoi(retTok.val)
					astBody = append(astBody, &LetStmt{
						Name: synthName,
						Value: &CallExpr{
							Name: "set_number",
							Args: []Expr{
								&LiteralExpr{Value: false},
								&LiteralExpr{Value: map[string]any{"num": num}},
							},
						},
					})
					rets = append(rets, synthName)
				default:
					return p.errorf(retTok.pos, "expected identifier, number, or null in return list, got %s", retTok.describe())
				}
				sep, err := p.next()
				if err != nil {
					return err
				}
				if sep.kind != tokComma {
					p.unget(sep)
					break
				}
			}
			continue
		}

		// Handle let statements in fn bodies
		if tok.val == "let" {
			varTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			sep, err := p.next()
			if err != nil {
				return err
			}
			if sep.kind == tokComma {
				// Multi-return: let a, b, _ = fnCall args... OR instruction
				bindings := []MultiBinding{{Name: varTok.val}}
				for {
					bindTok, err := p.next()
					if err != nil {
						return err
					}
					if bindTok.kind != tokIdent {
						return p.errorf(bindTok.pos, "expected identifier or '_' in binding list, got %s", bindTok.describe())
					}
					if bindTok.val == "_" {
						bindings = append(bindings, MultiBinding{Discard: true})
					} else {
						bindings = append(bindings, MultiBinding{Name: bindTok.val})
					}
					next, err := p.next()
					if err != nil {
						return err
					}
					if next.kind == tokEquals {
						break
					}
					if next.kind != tokComma {
						return p.errorf(next.pos, "expected ',' or '=' in binding list, got %s", next.describe())
					}
				}
				calleeTok, err := p.expect(tokIdent)
				if err != nil {
					return err
				}
				if calleeTok.val == "instruction" {
					frame, err := p.parseInstruction()
					if err != nil {
						return err
					}
					retCount := frameReturnCount(frame)
					if retCount == 0 {
						return p.errorf(calleeTok.pos, "instruction has no return slots (@N); cannot assign its result")
					}
					if len(bindings) > retCount {
						return p.errorf(calleeTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
					}
					if err := p.checkFnBodyInstructionDirections(frame, paramDirs, calleeTok.pos); err != nil {
						return err
					}
					for _, bind := range bindings {
						if !bind.Discard {
							letVars[bind.Name] = true
						}
					}
					astBody = append(astBody, &MultiReturnStmt{
						Bindings: bindings,
						Value:    &InstructionExpr{Frame: frame},
						Comment:  comment,
					})
					continue
				}
				callee := p.fns[calleeTok.val]
				if callee == nil {
					return p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
				}
				if !callee.hasReturn() {
					return p.errorf(calleeTok.pos, "function %q has no return value", calleeTok.val)
				}
				if len(bindings) > callee.returnCount() {
					return p.errorf(calleeTok.pos, "too many bindings (%d) for function %q which returns %d values", len(bindings), calleeTok.val, callee.returnCount())
				}
				args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, paramDirs, letVars)
				if err != nil {
					return err
				}
				for _, bind := range bindings {
					if !bind.Discard {
						letVars[bind.Name] = true
					}
				}
				astBody = append(astBody, &MultiReturnStmt{
					Bindings: bindings,
					Value: &CallExpr{
						Name:   calleeTok.val,
						Args:   args,
						KwArgs: kwArgs,
					},
					Comment: comment,
				})
				continue
			}
			// Single return: let varName = fnCall / Constructor / instruction
			if sep.kind != tokEquals {
				return p.errorf(sep.pos, "expected ',' or '=' after let identifier, got %s", sep.describe())
			}
			rhsTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}

			// Check for instruction RHS
			if rhsTok.val == "instruction" {
				frame, err := p.parseInstruction()
				if err != nil {
					return err
				}
				if err := p.checkFnBodyInstructionDirections(frame, paramDirs, rhsTok.pos); err != nil {
					return err
				}
				if !frameHasReturnSlot(frame) {
					return p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
				}
				letVars[varTok.val] = true
				astBody = append(astBody, &LetStmt{
					Name:    varTok.val,
					Value:   &InstructionExpr{Frame: frame},
					Comment: comment,
				})
				continue
			}

			// Check for constructor RHS
			if isConstructor(rhsTok.val) {
				ctor, err := p.parseFnBodyConstructorExpr(rhsTok)
				if err != nil {
					return err
				}
				// Check for & operator
				peek, err := p.next()
				if err != nil {
					return err
				}
				var value Expr = ctor
				if peek.kind == tokAmpersand {
					numExpr, err := p.parseFnBodyExpr()
					if err != nil {
						return err
					}
					value = &AmpersandExpr{Value: ctor, Num: numExpr}
				} else {
					p.unget(peek)
				}
				letVars[varTok.val] = true
				astBody = append(astBody, &LetStmt{
					Name:    varTok.val,
					Value:   value,
					Comment: comment,
				})
				continue
			}

			callee := p.fns[rhsTok.val]
			if callee == nil {
				return p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
			}
			if !callee.hasReturn() {
				return p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
			}
			args, kwArgs, err := p.parseFnBodyCallArgs(callee, rhsTok, paramDirs, letVars)
			if err != nil {
				return err
			}
			letVars[varTok.val] = true
			astBody = append(astBody, &LetStmt{
				Name: varTok.val,
				Value: &CallExpr{
					Name:   rhsTok.val,
					Args:   args,
					KwArgs: kwArgs,
				},
				Comment: comment,
			})
			continue
		}

		// Bare function call
		callee := p.fns[tok.val]
		if callee == nil {
			return p.errorf(tok.pos, "unknown function %q", tok.val)
		}
		args, kwArgs, err := p.parseFnBodyCallArgs(callee, tok, paramDirs, letVars)
		if err != nil {
			return err
		}
		astBody = append(astBody, &CallStmt{
			Name:    tok.val,
			Args:    args,
			KwArgs:  kwArgs,
			Comment: comment,
		})
	}

	// Pure-instruction optimization: if the function body is a single
	// instruction frame, promote it to fnDef.frame for the fast direct-frame
	// expansion path.
	if len(astBody) == 1 {
		if instrStmt, ok := astBody[0].(*InstructionStmt); ok {
			frame := instrStmt.Frame
			canPromote := true
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
				canPromote = false
				break
			}
			if canPromote {
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
				p.fns[nameTok.val] = &fnDef{params: params, frame: promoted}
				return nil
			}
		}
	}

	p.fns[nameTok.val] = &fnDef{params: params, rets: rets, astBody: astBody}
	return nil
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
		return p.errorf(pos, "unknown statement %q", name)
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

	if err := p.emitFnBody(fn.astBody, b, paramMap, usedVars, comment, pos); err != nil {
		return err
	}
	for _, rc := range retCopies {
		f := map[string]any{"op": "set_reg", "1": rc.from, "2": rc.to}
		setComment(f, comment)
		b.emit(f)
	}
	return nil
}
