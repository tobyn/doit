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

// --- Continuation block parsing ---

// parseContinuationBlocks parses continuation blocks after a function call.
// The opening '{' has already been consumed. bodyParser parses a statement
// block body (receives no args, returns []Stmt).
//
// Two forms:
//   Multi-block:  { name1 { body } name2 { body } }
//   Collapsed:    { body }   (name="" → leftmost exec name)
//
// Disambiguation: if token after '{' is an ident followed by '{' or 'for'
// followed by an ident, it's multi-block. Otherwise collapsed.
func (p *parser) parseContinuationBlocks(execNames []string, execDetached map[string]bool, bodyParser func(params []string, detached bool) ([]Stmt, error)) ([]*ContinuationBlock, error) {
	// Disambiguate multi-block vs collapsed form.
	//
	// Multi-block: { name1 { body } name2 { body } ... }
	// Collapsed:   { body }   or   { var -> body }
	//
	// Use save/restore to peek ahead without token loss.
	saved := p.save()
	tok1, err := p.next()
	if err != nil {
		return nil, err
	}

	if tok1.kind == tokIdent {
		tok2, err := p.next()
		if err != nil {
			return nil, err
		}

		// Multi-block: ident followed by '{' means named block.
		if tok2.kind == tokLBrace {
			p.unget(tok2)
			return p.parseContinuationBlocksMulti(execNames, execDetached, bodyParser, tok1)
		}
	}

	// Not multi-block — collapsed unnamed form.
	p.restore(saved)

	// Try Kotlin-style bindings
	params, err := p.tryParseContBlockBindings()
	if err != nil {
		return nil, err
	}

	// Collapsed form inherits detached from the leftmost exec name
	detached := execDetached[execNames[0]]
	body, err := bodyParser(params, detached)
	return []*ContinuationBlock{{Name: "", Params: params, Body: body, Detached: detached}}, nil
}

// parseContinuationBlocksMulti parses the multi-block form:
//   name1 { body } name2 { body } ...
// Outer '}' terminates.
func (p *parser) parseContinuationBlocksMulti(execNames []string, execDetached map[string]bool, bodyParser func(params []string, detached bool) ([]Stmt, error), firstTok token) ([]*ContinuationBlock, error) {
	var blocks []*ContinuationBlock
	seen := map[string]bool{}

	// firstTok is the first token already consumed by the caller's
	// disambiguation logic. Process it as the first loop iteration.
	first := true
	for {
		var tok token
		var err error
		if first {
			tok = firstTok
			first = false
		} else {
			tok, err = p.next()
			if err != nil {
				return nil, err
			}
		}
		if tok.kind == tokRBrace {
			break // end of multi-block
		}

		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected continuation name or '}', got %s", tok.describe())
		}
		name := tok.val
		if Keywords[name] {
			return nil, p.errorf(tok.pos, "expected continuation name, got keyword %q", name)
		}

		// Validate name against exec list
		found := false
		for _, en := range execNames {
			if en == name {
				found = true
				break
			}
		}
		if !found {
			return nil, p.errorf(tok.pos, "unknown continuation %q (function declares exec(%s))", name, strings.Join(execNames, ", "))
		}
		if seen[name] {
			return nil, p.errorf(tok.pos, "duplicate continuation block %q", name)
		}
		seen[name] = true

		if _, err := p.expect(tokLBrace); err != nil {
			return nil, err
		}

		// Try Kotlin-style bindings: ident -> or ident, ident, ... ->
		params, err := p.tryParseContBlockBindings()
		if err != nil {
			return nil, err
		}

		isDetached := execDetached[name]
		body, err := bodyParser(params, isDetached)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, &ContinuationBlock{Name: name, Params: params, Body: body, Detached: isDetached})
	}

	if len(blocks) == 0 {
		return nil, p.errorf(0, "empty continuation block")
	}
	return blocks, nil
}

// tryParseContBlockBindings tries to parse Kotlin-style variable bindings
// at the start of a continuation block body: `var ->` or `a, b ->`.
// If bindings are found, returns the parameter names. If no arrow is found,
// restores scanner state and returns nil.
func (p *parser) tryParseContBlockBindings() ([]string, error) {
	saved := p.save()

	var names []string
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind != tokIdent {
			p.restore(saved)
			return nil, nil
		}
		if Keywords[tok.val] {
			p.restore(saved)
			return nil, nil
		}
		names = append(names, tok.val)

		sep, err := p.next()
		if err != nil {
			return nil, err
		}
		if sep.kind == tokArrow {
			return names, nil
		}
		if sep.kind != tokComma {
			p.restore(saved)
			return nil, nil
		}
	}
}

// buildExecBindingMap builds a map from continuation name to execBinding for a
// function. It collects bindings from two sources:
//  1. Instruction block exec bindings (instruction-based functions)
//  2. Synthetic bindings from execContArgs (pure-logic functions with data dispatch)
//
// Instruction bindings take priority over synthetic ones.
func buildExecBindingMap(fn *fnDef) map[string]execBinding {
	ebMap := map[string]execBinding{}
	// Source 1: instruction block exec bindings
	for _, s := range fn.astBody {
		instrStmt, ok := s.(*InstructionStmt)
		if !ok {
			continue
		}
		for _, v := range instrStmt.Frame {
			if eb, ok := v.(execBinding); ok {
				ebMap[eb.name] = eb
			}
		}
	}
	// Source 2: synthetic from pure-logic continuation args
	for name, count := range fn.execContArgs {
		if _, exists := ebMap[name]; exists {
			continue // instruction binding takes priority
		}
		args := make([]any, count)
		for i := 0; i < count; i++ {
			args[i] = returnSlot(i + 1)
		}
		ebMap[name] = execBinding{name: name, args: args}
	}
	return ebMap
}

// allocExecOutputRegs builds a mapping from returnSlot(N) indices to register
// names for a branching function's exec output data. If a slot already has a
// register assigned via fn.rets + paramMap (from the caller's retVals), that
// register is reused. Otherwise, a new register is allocated using block param
// names when available, falling back to "@out". Newly allocated registers are
// written back to paramMap so resolveInstructionFrame can resolve them.
func allocExecOutputRegs(fn *fnDef, blocks []*ContinuationBlock, maxSlot int, paramMap map[string]any, usedVars map[string]bool) map[int]string {
	regs := map[int]string{}

	ebMap := buildExecBindingMap(fn)

	// First pass: reuse registers already assigned via fn.rets + paramMap
	for i := 1; i <= maxSlot; i++ {
		retIdx := i - 1
		if retIdx < len(fn.rets) {
			if target, ok := paramMap[fn.rets[retIdx]].(string); ok {
				regs[i] = target
			}
		}
	}

	// Second pass: allocate new registers for unassigned slots, using
	// block param names when available. Param names are used directly
	// (not through allocUniqueVar) because at behavior level the param
	// name IS the register name — it was already declared in scope
	// during parsing.
	for _, blk := range blocks {
		eb := ebMap[blk.Name]
		for i, paramName := range blk.Params {
			if i >= len(eb.args) {
				break
			}
			rs, ok := eb.args[i].(returnSlot)
			if !ok {
				continue
			}
			slot := int(rs)
			if _, already := regs[slot]; already {
				continue // slot already assigned
			}
			regs[slot] = paramName
			usedVars[paramName] = true
		}
	}

	// Fill remaining slots with generic names
	for i := 1; i <= maxSlot; i++ {
		if _, ok := regs[i]; !ok {
			regs[i] = allocUniqueVar("@out", usedVars)
		}
	}

	// Write newly allocated registers back to paramMap so
	// resolveInstructionFrame can resolve returnSlot values to them
	for i := 1; i <= maxSlot; i++ {
		retIdx := i - 1
		if retIdx < len(fn.rets) {
			paramMap[fn.rets[retIdx]] = regs[i]
		}
	}

	return regs
}

// findMaxExecOutputSlot scans AST bodies for InstructionStmt frames with
// execBinding args and returns the highest returnSlot index used. Returns 0
// if no exec bindings have data args.
func findMaxExecOutputSlot(stmts []Stmt) int {
	max := 0
	for _, s := range stmts {
		instrStmt, ok := s.(*InstructionStmt)
		if !ok {
			continue
		}
		for _, v := range instrStmt.Frame {
			eb, ok := v.(execBinding)
			if !ok {
				continue
			}
			for _, arg := range eb.args {
				if rs, ok := arg.(returnSlot); ok {
					if int(rs) > max {
						max = int(rs)
					}
				}
			}
		}
	}
	return max
}

// findMaxReturnSlot scans AST bodies for InstructionStmt frames with
// direct returnSlot values (not inside exec bindings) and returns the
// highest index used. Returns 0 if none found.
func findMaxReturnSlot(stmts []Stmt) int {
	max := 0
	for _, s := range stmts {
		instrStmt, ok := s.(*InstructionStmt)
		if !ok {
			continue
		}
		for _, v := range instrStmt.Frame {
			if rs, ok := v.(returnSlot); ok {
				if int(rs) > max {
					max = int(rs)
				}
			}
		}
	}
	return max
}

// parseExecBindingArgs parses the argument list after a continuation name
// in an instruction block: name(@1, @2) or return(@1). The opening '('
// has already been consumed. Returns the list of args (returnSlot values,
// integers, false for null).
func (p *parser) parseExecBindingArgs() ([]any, error) {
	var args []any
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRParen {
			break
		}
		if len(args) > 0 {
			if tok.kind != tokComma {
				return nil, p.errorf(tok.pos, "expected ',' or ')' in exec binding args, got %s", tok.describe())
			}
			tok, err = p.next()
			if err != nil {
				return nil, err
			}
		}
		switch tok.kind {
		case tokAt:
			numTok, err := p.expect(tokNumber)
			if err != nil {
				return nil, err
			}
			n, _ := strconv.Atoi(numTok.val)
			if n < 1 {
				return nil, p.errorf(numTok.pos, "@N index must be >= 1, got @%d", n)
			}
			args = append(args, returnSlot(n))
		case tokNumber:
			n, _ := strconv.Atoi(tok.val)
			args = append(args, n)
		case tokIdent:
			if tok.val == "null" || tok.val == "false" {
				args = append(args, false)
			} else {
				return nil, p.errorf(tok.pos, "expected @N, number, or null in exec binding args, got identifier %q", tok.val)
			}
		default:
			return nil, p.errorf(tok.pos, "expected @N, number, or null in exec binding args, got %s", tok.describe())
		}
	}
	if len(args) == 0 {
		return nil, p.errorf(0, "empty exec binding argument list")
	}
	return args, nil
}

// expandContinuationBlocks emits continuation block bodies after the
// function body and patches exec binding slots in the instruction frame
// to point to the block starts. Bridging blocks get a jump to the join
// point after their body. emitBody is called to emit each block's
// statement list with optional param bindings. execOutputRegs maps
// returnSlot(N) indices to allocated register names (nil if no data).
// emitTail, if non-nil, is called after each block's body to emit
// the block's tail expression to the return target (expression form).
func (p *parser) expandContinuationBlocks(fn *fnDef, blocks []*ContinuationBlock, b *frameBuilder, origPos int, emitBody func([]Stmt, map[string]any) error, execOutputRegs map[int]string, emitTail func(Expr) error) error {
	// Resolve collapsed form: empty name → leftmost exec name
	for _, blk := range blocks {
		if blk.Name == "" {
			blk.Name = fn.execNames[0]
		}
	}

	execBindings := buildExecBindingMap(fn)

	// Validate block param counts against continuation data arg counts
	for _, blk := range blocks {
		if len(blk.Params) == 0 {
			continue
		}
		eb := execBindings[blk.Name]
		if len(blk.Params) > len(eb.args) {
			if len(eb.args) == 0 {
				return fmt.Errorf("continuation %q does not provide data, but block has %d binding(s)", blk.Name, len(blk.Params))
			}
			return fmt.Errorf("continuation %q provides %d data arg(s), but block has %d binding(s)", blk.Name, len(eb.args), len(blk.Params))
		}
	}

	// Emit each block's body, recording start positions
	blockStarts := map[string]int{}
	bridgeJumps := []int{} // indices of bridge-jump frames to patch

	for _, blk := range blocks {
		blockStarts[blk.Name] = b.pos()

		// Build param bindings: map block param names to register names
		var bindings map[string]any
		if len(blk.Params) > 0 && execOutputRegs != nil {
			eb := execBindings[blk.Name]
			bindings = map[string]any{}
			for i, paramName := range blk.Params {
				if i < len(eb.args) {
					if rs, ok := eb.args[i].(returnSlot); ok {
						if reg, ok := execOutputRegs[int(rs)]; ok {
							bindings[paramName] = reg
						}
					}
				}
			}
		}

		if err := emitBody(blk.Body, bindings); err != nil {
			return err
		}

		// Emit tail expression for expression-form blocks
		if blk.Tail != nil && emitTail != nil {
			if err := emitTail(blk.Tail); err != nil {
				return err
			}
		}

		if blk.Detached {
			// Detached block: set "next": false on last body frame.
			// This tells the VM to re-dispatch to the iterator.
			lastIdx := b.pos() - 1
			if lastIdx >= blockStarts[blk.Name] {
				b.frames[lastIdx]["next"] = false
			}
			// Patch @break to a noop that re-dispatches to the iterator.
			for j := blockStarts[blk.Name]; j < b.pos(); j++ {
				if op, _ := b.frames[j]["op"].(string); op == "@break" {
					b.frames[j] = map[string]any{
						"op":   "set_reg",
						"1":    false,
						"2":    false,
						"next": false,
					}
				}
			}
		} else {
			// Bridging block: jump to join point after body
			jumpIdx := b.emit(map[string]any{
				"op": "set_reg",
				"1":  false,
				"2":  false,
				// "next" will be patched to join point
			})
			bridgeJumps = append(bridgeJumps, jumpIdx)
			// Patch @break to jump to join point
			for j := blockStarts[blk.Name]; j < b.pos(); j++ {
				if op, _ := b.frames[j]["op"].(string); op == "@break" {
					b.frames[j] = map[string]any{
						"op": "set_reg",
						"1":  false,
						"2":  false,
					}
					bridgeJumps = append(bridgeJumps, j)
				}
			}
		}
	}

	// Join point = current position after all blocks
	joinPoint := b.pos()

	// Patch bridge jumps to point to join point
	for _, idx := range bridgeJumps {
		b.frames[idx]["next"] = frameRef(joinPoint)
	}

	// Patch exec binding slots in instruction frames within the function body
	bodyEnd := blockStarts[blocks[0].Name]
	for j := origPos; j < bodyEnd; j++ {
		f := b.frames[j]
		for k, v := range f {
			eb, ok := v.(execBinding)
			if !ok {
				continue
			}
			if start, hasBlock := blockStarts[eb.name]; hasBlock {
				f[k] = frameRef(start)
			} else if eb.name == "return" {
				f[k] = frameRef(joinPoint)
			} else {
				// Unprovided continuation → bridge to join point
				f[k] = frameRef(joinPoint)
			}
		}
	}

	// Patch @return and @exec_<name> placeholders in the function body
	for j := origPos; j < joinPoint; j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@return" {
			b.frames[j] = map[string]any{
				"op":   "set_reg",
				"1":    false,
				"2":    false,
				"next": frameRef(joinPoint),
			}
		}
		// Patch @exec_<name> continuation dispatch placeholders
		if next, ok := f["next"].(string); ok && strings.HasPrefix(next, "@exec_") {
			contName := next[len("@exec_"):]
			if start, hasBlock := blockStarts[contName]; hasBlock {
				f["next"] = frameRef(start)
			} else {
				// Unprovided continuation → bridge to join point
				f["next"] = frameRef(joinPoint)
			}
		}
	}

	return nil
}

// expandInstructionBlocks wires up local continuation blocks for an
// instruction with ' exec bindings. Simpler than expandContinuationBlocks
// because there is a single instruction frame (no function body range to scan)
// and no @return/@exec_ patching.
func (p *parser) expandInstructionBlocks(instrIdx int, blocks []*ContinuationBlock, b *frameBuilder, localNames []string, localDetached map[string]bool, emitBody func([]Stmt, map[string]any) error, execOutputRegs map[int]string, emitTail func(Expr) error) error {
	// Resolve collapsed form: empty name → leftmost local exec name
	for _, blk := range blocks {
		if blk.Name == "" {
			blk.Name = localNames[0]
		}
	}

	// Build exec binding map from the instruction frame (local bindings only)
	ebMap := map[string]execBinding{}
	for _, v := range b.frames[instrIdx] {
		if eb, ok := v.(execBinding); ok && eb.local {
			ebMap[eb.name] = eb
		}
	}

	// Validate block param counts
	for _, blk := range blocks {
		if len(blk.Params) == 0 {
			continue
		}
		eb := ebMap[blk.Name]
		if len(blk.Params) > len(eb.args) {
			if len(eb.args) == 0 {
				return fmt.Errorf("continuation %q does not provide data, but block has %d binding(s)", blk.Name, len(blk.Params))
			}
			return fmt.Errorf("continuation %q provides %d data arg(s), but block has %d binding(s)", blk.Name, len(eb.args), len(blk.Params))
		}
	}

	// Emit each block's body, recording start positions
	blockStarts := map[string]int{}
	bridgeJumps := []int{}

	for _, blk := range blocks {
		blockStarts[blk.Name] = b.pos()

		// Build param bindings: map block param names to register names
		var bindings map[string]any
		if len(blk.Params) > 0 && execOutputRegs != nil {
			eb := ebMap[blk.Name]
			bindings = map[string]any{}
			for i, paramName := range blk.Params {
				if i < len(eb.args) {
					if rs, ok := eb.args[i].(returnSlot); ok {
						if reg, ok := execOutputRegs[int(rs)]; ok {
							bindings[paramName] = reg
						}
					}
				}
			}
		}

		if err := emitBody(blk.Body, bindings); err != nil {
			return err
		}

		// Emit tail expression for expression-form blocks
		if blk.Tail != nil && emitTail != nil {
			if err := emitTail(blk.Tail); err != nil {
				return err
			}
		}

		if blk.Detached {
			// Detached block: set "next": false on last body frame.
			lastIdx := b.pos() - 1
			if lastIdx >= blockStarts[blk.Name] {
				b.frames[lastIdx]["next"] = false
			}
			// Patch @break to a noop that re-dispatches.
			for j := blockStarts[blk.Name]; j < b.pos(); j++ {
				if op, _ := b.frames[j]["op"].(string); op == "@break" {
					b.frames[j] = map[string]any{
						"op":   "set_reg",
						"1":    false,
						"2":    false,
						"next": false,
					}
				}
			}
		} else {
			// Bridging block: jump to join point after body
			jumpIdx := b.emit(map[string]any{
				"op": "set_reg",
				"1":  false,
				"2":  false,
			})
			bridgeJumps = append(bridgeJumps, jumpIdx)
			// Patch @break to jump to join point
			for j := blockStarts[blk.Name]; j < b.pos(); j++ {
				if op, _ := b.frames[j]["op"].(string); op == "@break" {
					b.frames[j] = map[string]any{
						"op": "set_reg",
						"1":  false,
						"2":  false,
					}
					bridgeJumps = append(bridgeJumps, j)
				}
			}
		}
	}

	// Join point
	joinPoint := b.pos()

	// Patch bridge jumps
	for _, idx := range bridgeJumps {
		b.frames[idx]["next"] = frameRef(joinPoint)
	}

	// Patch LOCAL exec bindings only in the instruction frame at instrIdx
	f := b.frames[instrIdx]
	for k, v := range f {
		eb, ok := v.(execBinding)
		if !ok || !eb.local {
			continue
		}
		if start, hasBlock := blockStarts[eb.name]; hasBlock {
			f[k] = frameRef(start)
		} else {
			// Unprovided local continuation → bridge to join point
			f[k] = frameRef(joinPoint)
		}
	}

	return nil
}

// --- Stdlib ---

func parseStdlib(stdlib fs.FS) (map[string]*fnDef, map[string]*iterDef, map[string]*enumDef, error) {
	matches, err := fs.Glob(stdlib, "*.doit")
	if err != nil {
		return nil, nil, nil, err
	}

	fns := map[string]*fnDef{}
	iters := map[string]*iterDef{}
	enums := map[string]*enumDef{}
	for _, p := range matches {
		if p == "prelude.doit" {
			continue
		}
		data, err := fs.ReadFile(stdlib, p)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := parseStdlibFile(string(data), fns, iters, enums); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return fns, iters, enums, nil
}

func isDirection(val string) bool {
	return val == "in" || val == "out" || val == "inout"
}

// --- Iterator declarations ---

// parseIterDecl parses an iter declaration:
//
//	iter name(params) -> out1, out2 { body }
//
// Returns the iterator name.
func (p *parser) parseIterDecl(private bool) (string, error) {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	if Keywords[nameTok.val] {
		return "", p.errorf(nameTok.pos, "%q is a reserved keyword and cannot be used as an iterator name", nameTok.val)
	}
	// Check collision with fns, consts, enums, and existing iters
	if err := p.checkDeclName(nameTok.val, "iterator", nameTok.pos); err != nil {
		return "", err
	}
	if _, exists := p.iters[nameTok.val]; exists {
		return "", p.errorf(nameTok.pos, "duplicate iterator %q", nameTok.val)
	}

	params, err := p.parseParamList()
	if err != nil {
		return "", err
	}

	// Parse -> output names
	if _, err := p.expect(tokArrow); err != nil {
		return "", p.errorf(nameTok.pos, "iter declaration requires '-> output_names' after parameters")
	}

	var outputs []string
	for {
		outTok, err := p.expect(tokIdent)
		if err != nil {
			return "", err
		}
		if Keywords[outTok.val] {
			return "", p.errorf(outTok.pos, "%q is a reserved keyword and cannot be used as an output name", outTok.val)
		}
		outputs = append(outputs, outTok.val)

		peek, err := p.next()
		if err != nil {
			return "", err
		}
		if peek.kind == tokComma {
			continue
		}
		p.unget(peek)
		break
	}
	if len(outputs) == 0 {
		return "", p.errorf(nameTok.pos, "iter declaration requires at least one output name after '->'")
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return "", err
	}

	// Build direction maps for enforcement in body
	paramDirs := map[string]string{}
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
	}
	ctx := &fnBodyContext{
		paramDirs: paramDirs,
		fnVarInfo: map[string]fnVarInfo{},
		inIter:    true,
		iterOutputs: outputs,
	}
	ctx.resolve = p.fnBodyResolver(ctx)

	// Enable function calls in boolean primary position
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

	// Check if this is a single instruction body (instruction-backed iter)
	if len(astBody) == 1 {
		if instrStmt, ok := astBody[0].(*InstructionStmt); ok {
			return p.buildInstructionIter(nameTok.val, params, outputs, instrStmt.Frame, private)
		}
	}

	// User-defined iter (yield-based): validate at least one yield
	if !bodyHasYield(astBody) {
		return "", p.errorf(nameTok.pos, "iter %q body must contain at least one 'yield' statement", nameTok.val)
	}

	p.iters[nameTok.val] = &iterDef{
		params:  params,
		outputs: outputs,
		astBody: astBody,
		private: private,
	}
	return nameTok.val, nil
}

// buildInstructionIter builds an instruction-backed iterDef from a parsed
// instruction frame inside an iter body. The frame uses simplified syntax:
// output names map to numbered slots, `done: N` signals exhaustion.
func (p *parser) buildInstructionIter(name string, params []paramDef, outputs []string, frame map[string]any, private bool) (string, error) {
	// Find the done slot and resolve output name → slot mappings.
	// In the iter instruction block:
	//   - numbered slots with output name values → output mappings
	//   - done: N → exhaustion exec slot
	//   - numbered slots with param name values → input mappings
	doneSlot := ""
	promoted := map[string]any{}
	for k, v := range frame {
		if k == "op" {
			promoted[k] = v
			continue
		}
		if k == "c" {
			// combo/mode field — pass through
			promoted[k] = v
			continue
		}
		if s, ok := v.(string); ok && s == "done" {
			// This is a `done: N` entry — but actually in our syntax it's reversed:
			// `done: 3` means the key is "done" and value is the slot number.
			// Wait — let me re-read the plan. The plan says:
			//   done: N specifies which exec slot signals exhaustion
			// So "done" is a key with a numeric value.
			// But that would clash with the instruction parser which stores key→value.
			// Actually looking at the frame map: key="done", value is an int.
			// We need to handle that case.
		}
	}

	// Re-parse: the instruction frame from parseInstruction stores entries as
	// key → value. For iter instruction blocks, we need:
	//   - "done" key with int value N → exec slot N-based ref for exhaustion
	//   - output name keys with int value → output name at that slot
	//   - param name keys → input parameter at that slot

	// Actually, the instruction was parsed by the standard parseInstruction.
	// For iter context, the simplified syntax has:
	//   N: output_name    (output mapping)
	//   N: param_name     (input mapping - same as fn)
	//   done: N           (exhaustion slot)
	//   c: mode           (combo field)
	//
	// The standard parseInstruction treats identifiers as string values.
	// So output names and param names are stored as strings in the frame.

	// Build param name set for disambiguation
	paramNames := map[string]bool{}
	for _, pd := range params {
		paramNames[pd.name] = true
	}
	outputNames := map[string]bool{}
	for _, out := range outputs {
		outputNames[out] = true
	}

	// Clear and rebuild
	promoted = map[string]any{"op": frame["op"]}

	for k, v := range frame {
		if k == "op" {
			continue
		}
		if k == "done" {
			// done: N — store the 0-based ref key for exhaustion
			if n, ok := v.(int); ok {
				doneSlot = strconv.Itoa(n)
			} else {
				return "", p.errorf(0, "iter %q: 'done' value must be a number", name)
			}
			continue
		}
		if k == "c" {
			promoted[k] = v
			continue
		}
		// Numbered slot
		if s, ok := v.(string); ok {
			if outputNames[s] {
				// Output mapping: this slot produces this output
				promoted[k] = s
				continue
			}
			if paramNames[s] {
				// Input mapping: this slot takes this param
				promoted[k] = s
				continue
			}
		}
		// Pass through other values (ints, etc.)
		promoted[k] = v
	}

	if doneSlot == "" {
		return "", p.errorf(0, "iter %q: instruction block requires 'done: N' to specify exhaustion slot", name)
	}

	p.iters[name] = &iterDef{
		params:   params,
		outputs:  outputs,
		frame:    promoted,
		doneSlot: doneSlot,
		private:  private,
	}
	return name, nil
}

// bodyHasYield reports whether a statement list contains at least one YieldStmt.
func bodyHasYield(stmts []Stmt) bool {
	for _, s := range stmts {
		switch st := s.(type) {
		case *YieldStmt:
			return true
		case *IfStmt:
			if bodyHasYield(st.Body) {
				return true
			}
			for _, elif := range st.ElseIfs {
				if bodyHasYield(elif.Body) {
					return true
				}
			}
			if bodyHasYield(st.Else) {
				return true
			}
		case *ForStmt:
			if bodyHasYield(st.Body) {
				return true
			}
		case *WhileStmt:
			if bodyHasYield(st.Body) {
				return true
			}
		case *LoopStmt:
			if bodyHasYield(st.Body) {
				return true
			}
		case *ModeBlockStmt:
			if bodyHasYield(st.Body) {
				return true
			}
		}
	}
	return false
}

// parseYieldStmt parses a yield statement: `yield expr, expr, ...`
func (p *parser) parseYieldStmt(ctx *fnBodyContext, comment string) (*YieldStmt, error) {
	var values []Expr
	for {
		expr, err := p.parseFnBodyArgExpr(ctx)
		if err != nil {
			return nil, err
		}
		values = append(values, expr)

		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind != tokComma {
			p.unget(peek)
			break
		}
	}

	if len(values) != len(ctx.iterOutputs) {
		return nil, p.errorf(0, "yield produces %d value(s), but iter declares %d output(s)", len(values), len(ctx.iterOutputs))
	}

	return &YieldStmt{Values: values, Comment: comment}, nil
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
		if _, ok := p.enums[e.Name]; ok {
			return p.errorf(pos, "enum %q requires '::' member access (e.g., %s::Member)", e.Name, e.Name)
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
			if _, ok := p.enums[tok.val]; ok {
				return nil, p.errorf(tok.pos, "enum %q requires '::' member access (e.g., %s::Member)", tok.val, tok.val)
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
		} else if e, ok := p.enums[tok.val]; ok {
			expr, err := p.parseEnumAccess(tok, e)
			if err != nil {
				return nil, err
			}
			base = expr
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
		mbe, err := p.parseModeBlockExpr(tok.val == "unlocked", p.fnParseCtx(ctx), "")
		if err != nil {
			return nil, err
		}
		return p.parseArithExprFromFull(Expr(mbe), ctx.resolve)
	}
	if tok.kind == tokIdent && tok.val == "if" {
		ifExpr, err := p.parseIfExpr(p.fnParseCtx(ctx), "")
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
		if i > 0 {
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
		if e.Blocks != nil {
			opts := expandCallOpts{
				blocks: e.Blocks,
				emitBlockBody: func(stmts []Stmt, bindings map[string]any) error {
					pm := maps.Clone(paramMap)
					for k, v := range bindings {
						pm[k] = v
					}
					return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
				},
				emitTail: func(tail Expr) error {
					return p.emitExprTo(tail, target, b, paramMap, usedVars, comment, pos)
				},
			}
			return p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{target}, b, pos, comment, usedVars, opts)
		}
		return p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{target}, b, pos, comment, usedVars)
	case *InstructionExpr:
		if e.Blocks != nil {
			return p.emitFnBodyInstructionExprWithBlocks(e, []any{target}, b, paramMap, usedVars, comment, pos)
		}
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
		ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
		return p.emitModeBlockExpr(e, []any{target}, ctx, comment)
	case *IfExpr:
		ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
		return p.emitIfExpr(e, []any{target}, ctx, comment)
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

// fnParseCtx constructs a parseContext for fn body parsing.
func (p *parser) fnParseCtx(ctx *fnBodyContext) *parseContext {
	type fnScopeState struct {
		info  map[string]fnVarInfo
		depth int
	}
	var scopeStack []fnScopeState
	return &parseContext{
		resolve: ctx.resolve,
		parseBody: func(exprTail bool) ([]Stmt, error) {
			if exprTail {
				return p.parseFnBodyStmtsInner(ctx, true)
			}
			return p.parseFnBodyStmts(ctx)
		},
		pushScope: func() {
			info, depth := ctx.pushFnScope()
			scopeStack = append(scopeStack, fnScopeState{info, depth})
		},
		popScope: func() {
			n := len(scopeStack) - 1
			s := scopeStack[n]
			ctx.popFnScope(s.info, s.depth)
			scopeStack = scopeStack[:n]
		},
		declareIterVar: func(name string) {
			ctx.declareFnVarWarn(name, false, p, 0)
		},
		parseConstructor: func(nameTok token) (Expr, error) {
			return p.parseFnBodyConstructorExpr(nameTok)
		},
	}
}

// fnEmitCtx constructs an emitContext for fn body emission.
func (p *parser) fnEmitCtx(b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) *emitContext {
	return &emitContext{
		b:        b,
		usedVars: usedVars,
		resolveBool: func(expr Expr) (*resolvedBoolExpr, error) {
			return p.resolveFnBoolTree(expr, b, paramMap, usedVars, pos)
		},
		emitBody: func(stmts []Stmt) error {
			return p.emitFnBody(stmts, b, paramMap, usedVars, comment, pos)
		},
		exprGetValue: func(expr Expr, cmt string) (any, error) {
			return p.emitExprGetValue(expr, b, paramMap, usedVars, cmt, pos)
		},
		exprTo: func(expr Expr, target any, cmt string) error {
			return p.emitExprTo(expr, target, b, paramMap, usedVars, cmt, pos)
		},
		expandCallExpr: func(ce *CallExpr, retVals []any, cmt string) error {
			resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(ce.Args, ce.KwArgs, b, paramMap, usedVars, pos)
			if err != nil {
				return err
			}
			return p.expandCall(ce.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, cmt, usedVars)
		},
		resolveInstrFrame: func(frame map[string]any, retVals []any, cmt string) map[string]any {
			return resolveInstructionFrame(frame, retVals, paramMap, nil, cmt)
		},
		pushScope:      func() {},
		popScope:       func() {},
		declareIterVar: func(name string) string {
			counterVar := allocUniqueVar(name, usedVars)
			paramMap[name] = counterVar
			return counterVar
		},
	}
}

// inheritComment returns stmtComment if non-empty, otherwise falls back to
// the caller's comment. Used in emitFnBody to propagate doc comments.
func inheritComment(stmtComment, callerComment string) string {
	if stmtComment != "" {
		return stmtComment
	}
	return callerComment
}

// emitFnBodyInstructionWithBlocks emits an instruction with local ' blocks
// inside a fn body expansion.
func (p *parser) emitFnBodyInstructionWithBlocks(s *InstructionStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	localNames, localDetached := extractLocalExecInfo(s.Frame)
	execOutputRegs := allocLocalExecOutputRegs(s.Frame, s.Blocks, usedVars)

	maxSlot := 0
	for slot := range execOutputRegs {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	retVals := make([]any, maxSlot)
	for slot, reg := range execOutputRegs {
		retVals[slot-1] = reg
	}

	resolved := resolveInstructionFrame(s.Frame, retVals, paramMap, nil, comment)
	instrIdx := b.emit(resolved)

	return p.expandInstructionBlocks(instrIdx, s.Blocks, b, localNames, localDetached,
		func(stmts []Stmt, bindings map[string]any) error {
			pm := maps.Clone(paramMap)
			for k, v := range bindings {
				pm[k] = v
			}
			return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
		},
		execOutputRegs, nil)
}

// emitFnBodyInstructionExprWithBlocks emits an instruction expression with
// local ' blocks inside a fn body expansion. Each block has a Tail expression
// that produces the block's value.
func (p *parser) emitFnBodyInstructionExprWithBlocks(e *InstructionExpr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	localNames, localDetached := extractLocalExecInfo(e.Frame)
	execOutputRegs := allocLocalExecOutputRegs(e.Frame, e.Blocks, usedVars)

	// Merge retVals and execOutputRegs for @N substitution
	maxSlot := 0
	for slot := range execOutputRegs {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	instrRetVals := make([]any, maxSlot)
	for i := 0; i < maxSlot; i++ {
		if i < len(retVals) {
			instrRetVals[i] = retVals[i]
		}
		if reg, ok := execOutputRegs[i+1]; ok {
			instrRetVals[i] = reg
		}
	}

	resolved := resolveInstructionFrame(e.Frame, instrRetVals, paramMap, nil, comment)
	instrIdx := b.emit(resolved)

	return p.expandInstructionBlocks(instrIdx, e.Blocks, b, localNames, localDetached,
		func(stmts []Stmt, bindings map[string]any) error {
			pm := maps.Clone(paramMap)
			for k, v := range bindings {
				pm[k] = v
			}
			return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
		},
		execOutputRegs,
		func(tail Expr) error {
			if len(retVals) > 0 {
				return p.emitExprTo(tail, retVals[0], b, paramMap, usedVars, "", pos)
			}
			return nil
		})
}

// emitFnBody emits frames for an AST body during call expansion.
func (p *parser) emitFnBody(stmts []Stmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	collectASTOutputVars(stmts, paramMap, usedVars)

	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *InstructionStmt:
			callComment := inheritComment(s.Comment, comment)
			if s.Blocks != nil {
				if err := p.emitFnBodyInstructionWithBlocks(s, b, paramMap, usedVars, callComment, pos); err != nil {
					return err
				}
			} else {
				resolved := resolveInstructionFrame(s.Frame, nil, paramMap, nil, callComment)
				b.emit(resolved)
			}

		case *ModeBlockStmt:
			callComment := inheritComment(s.Comment, comment)
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
			callComment := inheritComment(s.Comment, comment)
			if s.Blocks != nil {
				if err := p.expandCall(s.Name, resolvedArgs, resolvedKwArgs, nil, b, pos, callComment, usedVars, expandCallOpts{
					blocks: s.Blocks,
					emitBlockBody: func(stmts []Stmt, bindings map[string]any) error {
						pm := maps.Clone(paramMap)
						for k, v := range bindings {
							pm[k] = v
						}
						return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
					},
				}); err != nil {
					return err
				}
			} else {
				if err := p.expandCall(s.Name, resolvedArgs, resolvedKwArgs, nil, b, pos, callComment, usedVars); err != nil {
					return err
				}
			}

		case *LetStmt:
			target := resolveVarName(s.Name, paramMap)
			callComment := inheritComment(s.Comment, comment)
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
			callComment := inheritComment(s.Comment, comment)
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
				ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
				if err := p.emitModeBlockExpr(v, retVals, ctx, callComment); err != nil {
					return err
				}
			case *IfExpr:
				ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
				if err := p.emitIfExpr(v, retVals, ctx, callComment); err != nil {
					return err
				}
			case *InstructionExpr:
				if v.Blocks != nil {
					if err := p.emitFnBodyInstructionExprWithBlocks(v, retVals, b, paramMap, usedVars, callComment, pos); err != nil {
						return err
					}
				} else {
					resolved := resolveInstructionFrame(v.Frame, retVals, paramMap, nil, callComment)
					b.emit(resolved)
				}
			case *ExprListExpr:
				ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
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
								if err := p.emitModeBlockExpr(e, []any{retVals[bindIdx]}, ctx, callComment); err != nil {
									return err
								}
							}
						} else {
							mbeRetVals := retVals[bindIdx : bindIdx+mbeArity]
							if err := p.emitModeBlockExpr(e, mbeRetVals, ctx, callComment); err != nil {
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
								if err := p.emitIfExpr(e, []any{retVals[bindIdx]}, ctx, callComment); err != nil {
									return err
								}
							}
						} else {
							ifRetVals := retVals[bindIdx : bindIdx+ifArity]
							if err := p.emitIfExpr(e, ifRetVals, ctx, callComment); err != nil {
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
			callComment := inheritComment(s.Comment, comment)
			if err := p.emitExprTo(s.Value, target, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *CompoundAssignStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := inheritComment(s.Comment, comment)
			rhs, err := p.emitExprGetValue(s.Value, b, paramMap, usedVars, "", pos)
			if err != nil {
				return err
			}
			f := map[string]any{
				"op": arithOpName(s.Op),
				"1":  target,
				"2":  rhs,
				"3":  target,
			}
			setComment(f, callComment)
			b.emit(f)

		case *IncrDecrStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := inheritComment(s.Comment, comment)
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
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitIfStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *WhileStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitWhileStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *LoopStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitLoopStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *ForStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitForStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *WaitStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitWaitStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *BreakStmt:
			// Emit placeholder frame that emitLoopStmt/emitWhileStmt will patch
			f := map[string]any{"op": "@break"}
			if s.Label != "" {
				f["label"] = s.Label
			}
			b.emit(f)

		case *YieldBodyStmt:
			bodyStart := b.pos()
			if err := p.emitFnBody(s.Body, b, paramMap, usedVars, comment, pos); err != nil {
				return err
			}
			// If the body contains @continue placeholders, emit a bridge noop
			// and patch them to jump to it. Inside a loop, the loop emitter
			// will set next:false on the bridge (as the last body frame),
			// giving correct re-dispatch. At top level, the bridge falls
			// through sequentially to the next iterator statement.
			if hasContinuePlaceholder(b, bodyStart) {
				bridge := b.emit(map[string]any{"op": "set_reg", "1": false, "2": false})
				patchContinuePlaceholders(b, bodyStart, frameRef(bridge))
			}

		case *ContinueStmt:
			b.emit(map[string]any{"op": "@continue"})

		case *ExitStmt:
			callComment := inheritComment(s.Comment, comment)
			f := map[string]any{"op": "exit"}
			setComment(f, callComment)
			b.emit(f)

		case *RestartStmt:
			callComment := inheritComment(s.Comment, comment)
			f := map[string]any{"op": "restart"}
			setComment(f, callComment)
			b.emit(f)

		case *JumpStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitJumpStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *LastStmt:
			callComment := inheritComment(s.Comment, comment)
			f := map[string]any{"op": "last"}
			setComment(f, callComment)
			b.emit(f)

		case *ReturnStmt:
			callComment := inheritComment(s.Comment, comment)

			if s.Continuation != "" {
				// Emit data args to @carg registers before the jump
				for i, arg := range s.ContinuationArgs {
					target := resolveVarName("@carg"+strconv.Itoa(i+1), paramMap)
					if err := p.emitExprTo(arg, target, b, paramMap, usedVars, callComment, pos); err != nil {
						return err
					}
				}
				// Continuation dispatch: emit @exec_<name> placeholder
				b.emit(map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": "@exec_" + s.Continuation,
				})
			} else {
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
						if e.Blocks != nil {
							if err := p.emitFnBodyInstructionExprWithBlocks(e, retVals, b, paramMap, usedVars, callComment, pos); err != nil {
								return err
							}
						} else {
							resolved := resolveInstructionFrame(e.Frame, retVals, paramMap, nil, callComment)
							b.emit(resolved)
						}
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

func parseStdlibFile(src string, fns map[string]*fnDef, iters map[string]*iterDef, enums map[string]*enumDef) error {
	p := &parser{scanner: scanner{src: src}, fns: fns, iters: iters, enums: enums}
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return nil
		}
		if tok.kind == tokIdent && tok.val == "skip" {
			// Consume "skip prelude" directive
			next, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			if next.val != "prelude" {
				return p.errorf(next.pos, "expected 'prelude' after 'skip', got %s", next.describe())
			}
			continue
		}
		if tok.kind == tokIdent && tok.val == "enum" {
			if _, err := p.parseEnumDecl(false); err != nil {
				return err
			}
			continue
		}
		if tok.kind == tokIdent && tok.val == "iter" {
			if _, err := p.parseIterDecl(false); err != nil {
				return err
			}
			continue
		}
		if tok.kind != tokIdent || tok.val != "fn" {
			return p.errorf(tok.pos, "expected 'fn', 'iter', 'enum', or 'skip', got %s", tok.describe())
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

// --- Declaration collision checking ---

// checkDeclName checks that name doesn't collide with any existing function,
// constant, or enum declaration. kind is the type of the new declaration
// ("function", "constant", or "enum"). For functions, stdlib and imported
// names are excluded (user functions may override both).
func (p *parser) checkDeclName(name, kind string, pos int) error {
	if _, ok := p.fns[name]; ok {
		if kind == "function" {
			// Functions can override stdlib and imports; only duplicate
			// same-file user fns are errors.
			if p.stdlibFns[name] == nil && !p.importedNames[name] {
				return p.errorf(pos, "duplicate function %q", name)
			}
		} else {
			// Consts/enums cannot shadow any function (including stdlib)
			return p.errorf(pos, "%s %q conflicts with a function of the same name", kind, name)
		}
	}
	if _, ok := p.consts[name]; ok {
		if kind == "constant" {
			return p.errorf(pos, "duplicate constant %q", name)
		}
		return p.errorf(pos, "%s %q conflicts with a constant of the same name", kind, name)
	}
	if _, ok := p.enums[name]; ok {
		if kind == "enum" {
			return p.errorf(pos, "duplicate enum %q", name)
		}
		return p.errorf(pos, "%s %q conflicts with an enum of the same name", kind, name)
	}
	return nil
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
	if err := p.checkDeclName(name, "constant", nameTok.pos); err != nil {
		return "", err
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

// parseEnumDecl parses an enum declaration: enum Name { Member1; Member2 = 5; Member3 }
func (p *parser) parseEnumDecl(private bool) (string, error) {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	name := nameTok.val
	if Keywords[name] {
		return "", p.errorf(nameTok.pos, "%q is a reserved keyword and cannot be used as an enum name", name)
	}
	if name == "_" {
		return "", p.errorf(nameTok.pos, "'_' cannot be used as an enum name")
	}
	if err := p.checkDeclName(name, "enum", nameTok.pos); err != nil {
		return "", err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return "", err
	}

	values := map[string]int{}
	usedValues := map[int]string{} // value → member name (for duplicate value detection)
	var members []string
	nextVal := 0

	for {
		tok, err := p.next()
		if err != nil {
			return "", err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent {
			return "", p.errorf(tok.pos, "expected enum member name, got %s", tok.describe())
		}
		memberName := tok.val
		if Keywords[memberName] {
			return "", p.errorf(tok.pos, "%q is a reserved keyword and cannot be used as an enum member name", memberName)
		}
		if _, exists := values[memberName]; exists {
			return "", p.errorf(tok.pos, "duplicate enum member %q in %s", memberName, name)
		}

		// Check for explicit value assignment
		peek, err := p.next()
		if err != nil {
			return "", err
		}
		if peek.kind == tokEquals {
			// Explicit value: Member = N or Member = -N
			valTok, err := p.next()
			if err != nil {
				return "", err
			}
			negative := false
			if valTok.kind == tokMinus {
				negative = true
				valTok, err = p.next()
				if err != nil {
					return "", err
				}
			}
			if valTok.kind != tokNumber {
				return "", p.errorf(valTok.pos, "expected number for enum member value, got %s", valTok.describe())
			}
			num, _ := strconv.Atoi(valTok.val)
			if negative {
				num = -num
			}
			nextVal = num
		} else {
			p.unget(peek)
		}

		// Check for duplicate value
		if prevMember, exists := usedValues[nextVal]; exists {
			return "", p.errorf(tok.pos, "enum member %q has the same value (%d) as %q in %s", memberName, nextVal, prevMember, name)
		}

		values[memberName] = nextVal
		usedValues[nextVal] = memberName
		members = append(members, memberName)
		nextVal++

		// Optional comma separator between members.
		sep, err := p.next()
		if err != nil {
			return "", err
		}
		if sep.kind != tokComma {
			p.unget(sep)
		}
	}

	if len(members) == 0 {
		return "", p.errorf(nameTok.pos, "enum %q has no members", name)
	}

	p.enums[name] = &enumDef{values: values, members: members, private: private}
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
	continued  bool
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
					if status.continued {
						continue // restart iteration
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
					if status.continued {
						continue // restart iteration (re-evaluate condition)
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
				env[s.IterVars[0]] = map[string]any{"num": i}
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
					if status.continued {
						continue // restart iteration
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *BreakStmt:
			return &constEvalStatus{broke: true, breakLabel: s.Label}, true
		case *ContinueStmt:
			return &constEvalStatus{continued: true}, true
		case *ExitStmt:
			return nil, false // bail: runtime-only
		case *RestartStmt:
			return nil, false // bail: runtime-only
		case *JumpStmt:
			return nil, false // bail: runtime-only
		case *LastStmt:
			return nil, false // bail: runtime-only
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
			case "fn", "iter":
				if err := p.skipFnDef(); err != nil {
					return nil, err
				}
			case "const":
				if err := p.skipToNextDecl(); err != nil {
					return nil, err
				}
			case "enum":
				// Skip enum name + brace block
				if _, err := p.expect(tokIdent); err != nil {
					return nil, err
				}
				if err := p.skipBraceBlock(); err != nil {
					return nil, err
				}
			default:
				return nil, p.errorf(fnTok.pos, "expected 'fn', 'iter', 'const', or 'enum' after 'private', got %q", fnTok.val)
			}
		case "fn", "iter":
			if err := p.skipFnDef(); err != nil {
				return nil, err
			}
		case "const":
			// Skip const declarations in pass 2 (already processed in pass 1)
			if err := p.skipToNextDecl(); err != nil {
				return nil, err
			}
		case "enum":
			// Skip enum declarations in pass 2 (already processed in pass 1)
			if _, err := p.expect(tokIdent); err != nil {
				return nil, err
			}
			if err := p.skipBraceBlock(); err != nil {
				return nil, err
			}
		case "import":
			// Skip import statements in pass 2 (already processed in pass 1)
			if err := p.skipToNextDecl(); err != nil {
				return nil, err
			}
		case "skip":
			// Skip "skip prelude" directive in pass 2
			if err := p.skipToNextDecl(); err != nil {
				return nil, err
			}
		default:
			return nil, p.errorf(tok.pos, "expected 'behavior', 'fn', 'iter', 'const', 'enum', 'skip', or 'private', got %q", tok.val)
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

	return p.collectDecls(false)
}

// collectDecls scans top-level declarations (behavior, fn, const, private).
// When isImport is true, behavior IDs are skipped and no collision checking
// is performed. When false, behavior IDs are collected and same-file names
// are checked against imports.
func (p *parser) collectDecls(isImport bool) error {
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
			if !isImport {
				p.behaviorIDs = append(p.behaviorIDs, idTok.val)
			}
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
				p.fileDecls = append(p.fileDecls, name)
			case "iter":
				name, err := p.parseIterDecl(true)
				if err != nil {
					return err
				}
				p.fileDecls = append(p.fileDecls, name)
			case "const":
				name, err := p.parseConstDecl(true)
				if err != nil {
					return err
				}
				p.fileDecls = append(p.fileDecls, name)
			case "enum":
				name, err := p.parseEnumDecl(true)
				if err != nil {
					return err
				}
				p.fileDecls = append(p.fileDecls, name)
			default:
				return p.errorf(fnTok.pos, "expected 'fn', 'iter', 'const', or 'enum' after 'private', got %q", fnTok.val)
			}
		case "fn":
			name, err := p.parseUserFn()
			if err != nil {
				return err
			}
			p.fileDecls = append(p.fileDecls, name)
		case "iter":
			name, err := p.parseIterDecl(false)
			if err != nil {
				return err
			}
			p.fileDecls = append(p.fileDecls, name)
		case "const":
			name, err := p.parseConstDecl(false)
			if err != nil {
				return err
			}
			p.fileDecls = append(p.fileDecls, name)
		case "enum":
			name, err := p.parseEnumDecl(false)
			if err != nil {
				return err
			}
			p.fileDecls = append(p.fileDecls, name)
		case "skip":
			// Consume "skip prelude" directive
			next, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			if next.val != "prelude" {
				return p.errorf(next.pos, "expected 'prelude' after 'skip', got %s", next.describe())
			}
		case "import":
			return p.errorf(tok.pos, "import statements must appear before function and behavior declarations")
		default:
			return p.errorf(tok.pos, "expected 'behavior', 'fn', 'iter', 'const', 'enum', 'skip', or 'private', got %q", tok.val)
		}
	}

	if !isImport {
		return p.checkImportCollisions(p.fileDecls)
	}
	return nil
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
	execNames    []string // continuation names from exec(...) declaration (nil if none)
	inIter       bool     // true when parsing an iter body
	iterOutputs  []string // output names from iter -> declaration
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

// isExecName reports whether name is a declared continuation in exec(...).
func (ctx *fnBodyContext) isExecName(name string) bool {
	for _, en := range ctx.execNames {
		if en == name {
			return true
		}
	}
	return false
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
	if err := p.checkDeclName(nameTok.val, "function", nameTok.pos); err != nil {
		return "", err
	}

	params, err := p.parseParamList()
	if err != nil {
		return "", err
	}

	// Parse optional exec(...) continuation declaration
	var execNames []string
	tok, err := p.next()
	if err != nil {
		return "", err
	}
	if tok.kind == tokIdent && tok.val == "exec" {
		if _, err := p.expect(tokLParen); err != nil {
			return "", err
		}
		for {
			nameTok, err := p.next()
			if err != nil {
				return "", err
			}
			if nameTok.kind == tokRParen {
				if len(execNames) == 0 {
					return "", p.errorf(nameTok.pos, "exec() requires at least one continuation name")
				}
				break
			}
			if len(execNames) > 0 {
				if nameTok.kind != tokComma {
					return "", p.errorf(nameTok.pos, "expected ',' or ')' in exec list, got %s", nameTok.describe())
				}
				nameTok, err = p.next()
				if err != nil {
					return "", err
				}
			}
			if nameTok.kind != tokIdent || Keywords[nameTok.val] {
				return "", p.errorf(nameTok.pos, "expected continuation name, got %s", nameTok.describe())
			}
			// Check for duplicate exec names
			for _, existing := range execNames {
				if existing == nameTok.val {
					return "", p.errorf(nameTok.pos, "duplicate continuation name %q", nameTok.val)
				}
			}
			// Check for collision with param names
			for _, pd := range params {
				if pd.name == nameTok.val {
					return "", p.errorf(nameTok.pos, "continuation name %q conflicts with parameter name", nameTok.val)
				}
			}
			execNames = append(execNames, nameTok.val)
		}
		if _, err := p.expect(tokLBrace); err != nil {
			return "", err
		}
	} else if tok.kind != tokLBrace {
		return "", p.errorf(tok.pos, "expected '{' or 'exec', got %s", tok.describe())
	}

	// Build direction maps for enforcement in fn body
	paramDirs := map[string]string{} // param name -> effective direction
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
	}
	ctx := &fnBodyContext{
		paramDirs: paramDirs,
		fnVarInfo: map[string]fnVarInfo{},
		execNames: execNames,
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

	// Post-parse: validate exec bindings and derive execDetached
	var execDetached map[string]bool
	if len(execNames) > 0 {
		execDetached = map[string]bool{}
		if err := validateExecBindings(astBody, execNames, execDetached); err != nil {
			return "", err
		}
	}

	// Helper to build fnDef with exec fields (execContArgs set after post-parse analysis)
	var execContArgs map[string]int
	makeFnDef := func(fd *fnDef) *fnDef {
		fd.execNames = execNames
		fd.execDetached = execDetached
		fd.execContArgs = execContArgs
		return fd
	}

	// Pure-instruction promotion (no return): if the function body is a
	// single instruction frame, promote it to fnDef.frame for the fast
	// direct-frame expansion path.
	if len(astBody) == 1 {
		if instrStmt, ok := astBody[0].(*InstructionStmt); ok {
			if promoted := tryPromoteInstruction(instrStmt.Frame, params, nil); promoted != nil {
				p.fns[nameTok.val] = makeFnDef(&fnDef{params: params, frame: promoted})
				return nameTok.val, nil
			}
		}
	}

	// Post-parse analysis: determine return path from ReturnStmt nodes
	returns := collectReturnStmts(astBody)

	// Build execContArgs from continuation returns with data args
	for _, ret := range returns {
		if ret.Continuation == "" || ret.ContinuationArgs == nil {
			continue
		}
		count := len(ret.ContinuationArgs)
		if prev, ok := execContArgs[ret.Continuation]; ok {
			if prev != count {
				return "", p.errorf(0, "inconsistent arg count for continuation %q: %d vs %d", ret.Continuation, prev, count)
			}
		} else {
			if execContArgs == nil {
				execContArgs = map[string]int{}
			}
			execContArgs[ret.Continuation] = count
		}
	}

	var rets []string

	if len(returns) == 0 {
		// No explicit return. For exec functions, check if instruction
		// frames contain returnSlot values (from @N in data slots or
		// exec binding args). Convert them to synthetic names so
		// resolveInstructionFrame can resolve them through paramMap.
		if len(execNames) > 0 {
			maxSlot := findMaxReturnSlot(astBody)
			if maxSlot > 0 {
				for i := 1; i <= maxSlot; i++ {
					rets = append(rets, "@ret"+strconv.Itoa(i))
				}
				// Replace returnSlot values in instruction frames
				// with synthetic name references
				for _, s := range astBody {
					instrStmt, ok := s.(*InstructionStmt)
					if !ok {
						continue
					}
					for k, v := range instrStmt.Frame {
						if rs, ok := v.(returnSlot); ok {
							instrStmt.Frame[k] = "@ret" + strconv.Itoa(int(rs))
						}
					}
				}
			}
		}
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
							p.fns[nameTok.val] = makeFnDef(&fnDef{params: params, frame: canPromote})
							return nameTok.val, nil
						}
					}

					p.fns[nameTok.val] = makeFnDef(&fnDef{params: params, rets: rets, astBody: astBody})
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
				p.fns[nameTok.val] = makeFnDef(&fnDef{params: params, rets: rets, astBody: astBody})
				return nameTok.val, nil
			}
		}

		// Emit-and-jump path: multiple returns, returns in blocks,
		// or returns with literals/calls
		maxArity := 0
		for _, ret := range returns {
			a := p.returnStmtArity(ret)
			if a > maxArity {
				maxArity = a
			}
		}
		for i := 1; i <= maxArity; i++ {
			rets = append(rets, "@ret"+strconv.Itoa(i))
		}
		// Leave ReturnStmt nodes in the body for emitFnBody to handle
	}

	p.fns[nameTok.val] = makeFnDef(&fnDef{params: params, rets: rets, astBody: astBody})
	return nameTok.val, nil
}

// tryPromoteInstruction checks whether an instruction frame can be promoted
// to the fast fnDef.frame path. Returns the promoted frame, or nil.
func tryPromoteInstruction(frame map[string]any, params []paramDef, rets []string) map[string]any {
	opVal, _ := frame["op"].(string)
	for _, v := range frame {
		// Reject frames containing exec bindings — branching functions
		// must use the AST path.
		if _, isExec := v.(execBinding); isExec {
			return nil
		}
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

// hasLocalExecBindings reports whether the frame contains any local exec bindings.
func hasLocalExecBindings(frame map[string]any) bool {
	for _, v := range frame {
		if eb, ok := v.(execBinding); ok && eb.local {
			return true
		}
	}
	return false
}

// extractLocalExecInfo collects the names and detached flags of local exec
// bindings from an instruction frame.
func extractLocalExecInfo(frame map[string]any) (execNames []string, execDetached map[string]bool) {
	execDetached = map[string]bool{}
	seen := map[string]bool{}
	for _, v := range frame {
		eb, ok := v.(execBinding)
		if !ok || !eb.local {
			continue
		}
		if seen[eb.name] {
			continue
		}
		seen[eb.name] = true
		execNames = append(execNames, eb.name)
		if eb.detached {
			execDetached[eb.name] = true
		}
	}
	return execNames, execDetached
}

// allocLocalExecOutputRegs builds a mapping from returnSlot(N) indices to
// register names for local exec binding data args. Mirrors allocExecOutputRegs
// but scans the instruction frame directly instead of going through fnDef.
func allocLocalExecOutputRegs(frame map[string]any, blocks []*ContinuationBlock, usedVars map[string]bool) map[int]string {
	// Collect exec bindings from the frame
	ebMap := map[string]execBinding{}
	for _, v := range frame {
		if eb, ok := v.(execBinding); ok && eb.local {
			ebMap[eb.name] = eb
		}
	}

	regs := map[int]string{}

	// Allocate from block param names
	for _, blk := range blocks {
		eb := ebMap[blk.Name]
		for i, paramName := range blk.Params {
			if i >= len(eb.args) {
				break
			}
			rs, ok := eb.args[i].(returnSlot)
			if !ok {
				continue
			}
			slot := int(rs)
			if _, already := regs[slot]; already {
				continue
			}
			regs[slot] = paramName
			usedVars[paramName] = true
		}
	}

	// Find max slot for filling remaining
	maxSlot := 0
	for _, v := range frame {
		if eb, ok := v.(execBinding); ok && eb.local {
			for _, arg := range eb.args {
				if rs, ok := arg.(returnSlot); ok {
					if int(rs) > maxSlot {
						maxSlot = int(rs)
					}
				}
			}
		}
	}

	// Fill remaining slots with generic names
	for i := 1; i <= maxSlot; i++ {
		if _, exists := regs[i]; !exists {
			regs[i] = allocUniqueVar("@out", usedVars)
		}
	}

	return regs
}

// validateExecBindings scans InstructionStmt frames in the AST for execBinding
// values, validates that binding names exist in execNames (or are "return"),
// and populates the execDetached map.
func validateExecBindings(stmts []Stmt, execNames []string, execDetached map[string]bool) error {
	execSet := map[string]bool{}
	for _, name := range execNames {
		execSet[name] = true
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *InstructionStmt:
			for _, v := range s.Frame {
				eb, ok := v.(execBinding)
				if !ok {
					continue
				}
				if eb.local {
					continue // local bindings are handled by expandInstructionBlocks
				}
				if eb.name != "return" && !execSet[eb.name] {
					return fmt.Errorf("exec binding %q is not declared in the function's exec(...) list", eb.name)
				}
				if eb.detached && eb.name != "return" {
					execDetached[eb.name] = true
				}
			}
		case *ModeBlockStmt:
			if err := validateExecBindings(s.Body, execNames, execDetached); err != nil {
				return err
			}
		case *IfStmt:
			if err := validateExecBindings(s.Body, execNames, execDetached); err != nil {
				return err
			}
			for _, elif := range s.ElseIfs {
				if err := validateExecBindings(elif.Body, execNames, execDetached); err != nil {
					return err
				}
			}
			if err := validateExecBindings(s.Else, execNames, execDetached); err != nil {
				return err
			}
		}
	}
	return nil
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
func (p *parser) returnStmtArity(ret *ReturnStmt) int {
	arity := 0
	for _, v := range ret.Values {
		switch e := v.(type) {
		case *CallExpr:
			if fn := p.fns[e.Name]; fn != nil {
				arity += fn.returnCount()
			} else {
				arity++
			}
		case *IfExpr:
			arity += p.ifExprArity(e)
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
		return p.parseModeBlockExpr(tok.val == "unlocked", p.fnParseCtx(ctx), "")
	}

	// If-expression: return if cond { a } else { b }
	if tok.kind == tokIdent && tok.val == "if" {
		return p.parseIfExpr(p.fnParseCtx(ctx), "")
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


// maybeParseFnBodyContinuationBlocks peeks for '{' and parses continuation
// blocks at fn body level. Returns nil if no '{' follows.
func (p *parser) maybeParseFnBodyContinuationBlocks(fn *fnDef, ctx *fnBodyContext) ([]*ContinuationBlock, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokLBrace {
		p.unget(tok)
		return nil, nil
	}
	return p.parseContinuationBlocks(fn.execNames, fn.execDetached, func(params []string, detached bool) ([]Stmt, error) {
		saved, depth := ctx.pushFnScope()
		defer ctx.popFnScope(saved, depth)
		for _, name := range params {
			ctx.declareFnVar(name, false)
		}
		savedLoopDepth := p.loopDepth
		savedLoopLabels := p.loopLabels
		p.loopDepth = 0
		p.loopLabels = map[string]bool{}
		p.execBlockDepth++
		defer func() {
			p.loopDepth = savedLoopDepth
			p.loopLabels = savedLoopLabels
			p.execBlockDepth--
		}()
		return p.parseFnBodyStmts(ctx)
	})
}

// maybeParseFnBodyContinuationBlocksExpr peeks for '{' and parses
// continuation blocks in expression form (each block has a tail
// expression). Returns nil if no '{' follows. Detached blocks are
// rejected — expression form requires all bridging.
func (p *parser) maybeParseFnBodyContinuationBlocksExpr(fn *fnDef, ctx *fnBodyContext) ([]*ContinuationBlock, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokLBrace {
		p.unget(tok)
		return nil, nil
	}
	lbracePos := tok.pos
	blocks, err := p.parseContinuationBlocks(fn.execNames, fn.execDetached, func(params []string, detached bool) ([]Stmt, error) {
		saved, depth := ctx.pushFnScope()
		defer ctx.popFnScope(saved, depth)
		for _, name := range params {
			ctx.declareFnVar(name, false)
		}
		savedLoopDepth := p.loopDepth
		savedLoopLabels := p.loopLabels
		p.loopDepth = 0
		p.loopLabels = map[string]bool{}
		p.execBlockDepth++
		defer func() {
			p.loopDepth = savedLoopDepth
			p.loopLabels = savedLoopLabels
			p.execBlockDepth--
		}()
		return p.parseFnBodyStmtsInner(ctx, true) // exprTail=true
	})
	if err != nil {
		return nil, err
	}
	// Extract tails and validate
	for _, blk := range blocks {
		if blk.Detached {
			return nil, p.errorf(lbracePos, "detached continuation cannot be used in expression form")
		}
		if len(blk.Body) == 0 {
			return nil, p.errorf(lbracePos, "empty continuation expression block")
		}
		last := blk.Body[len(blk.Body)-1]
		tail, ok := last.(*exprTailStmt)
		if !ok {
			return nil, p.errorf(lbracePos, "last item in continuation expression block must be a value-producing expression")
		}
		blk.Tail = tail.Expr
		blk.Body = blk.Body[:len(blk.Body)-1]
	}
	return blocks, nil
}

// maybeParseFnBodyLocalBlocks peeks for '{' and parses local continuation
// blocks for an instruction with ' exec bindings at fn body level.
func (p *parser) maybeParseFnBodyLocalBlocks(frame map[string]any, ctx *fnBodyContext) ([]*ContinuationBlock, error) {
	localNames, localDetached := extractLocalExecInfo(frame)
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokLBrace {
		p.unget(tok)
		return nil, nil
	}
	return p.parseContinuationBlocks(localNames, localDetached, func(params []string, detached bool) ([]Stmt, error) {
		saved, depth := ctx.pushFnScope()
		defer ctx.popFnScope(saved, depth)
		for _, name := range params {
			ctx.declareFnVar(name, false)
		}
		savedLoopDepth := p.loopDepth
		savedLoopLabels := p.loopLabels
		p.loopDepth = 0
		p.loopLabels = map[string]bool{}
		p.execBlockDepth++
		defer func() {
			p.loopDepth = savedLoopDepth
			p.loopLabels = savedLoopLabels
			p.execBlockDepth--
		}()
		return p.parseFnBodyStmts(ctx)
	})
}

// maybeParseFnBodyLocalBlocksExpr peeks for '{' and parses local continuation
// blocks in expression form at fn body level. Detached blocks are rejected.
func (p *parser) maybeParseFnBodyLocalBlocksExpr(frame map[string]any, ctx *fnBodyContext) ([]*ContinuationBlock, error) {
	localNames, localDetached := extractLocalExecInfo(frame)
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokLBrace {
		p.unget(tok)
		return nil, nil
	}
	lbracePos := tok.pos
	blocks, err := p.parseContinuationBlocks(localNames, localDetached, func(params []string, detached bool) ([]Stmt, error) {
		saved, depth := ctx.pushFnScope()
		defer ctx.popFnScope(saved, depth)
		for _, name := range params {
			ctx.declareFnVar(name, false)
		}
		savedLoopDepth := p.loopDepth
		savedLoopLabels := p.loopLabels
		p.loopDepth = 0
		p.loopLabels = map[string]bool{}
		p.execBlockDepth++
		defer func() {
			p.loopDepth = savedLoopDepth
			p.loopLabels = savedLoopLabels
			p.execBlockDepth--
		}()
		return p.parseFnBodyStmtsInner(ctx, true) // exprTail=true
	})
	if err != nil {
		return nil, err
	}
	for _, blk := range blocks {
		if blk.Detached {
			return nil, p.errorf(lbracePos, "detached continuation cannot be used in expression form")
		}
		if len(blk.Body) == 0 {
			return nil, p.errorf(lbracePos, "empty continuation expression block")
		}
		last := blk.Body[len(blk.Body)-1]
		tail, ok := last.(*exprTailStmt)
		if !ok {
			return nil, p.errorf(lbracePos, "last item in continuation expression block must be a value-producing expression")
		}
		blk.Tail = tail.Expr
		blk.Body = blk.Body[:len(blk.Body)-1]
	}
	return blocks, nil
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
	var terminal Stmt // non-nil when the last statement was terminal
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if terminal != nil {
			p.warnf(tok.pos, "unreachable code after '%s'", terminalKeyword(terminal))
			p.unget(tok)
			if err := p.skipToCloseBrace(); err != nil {
				return nil, err
			}
			break
		}
		if tok.kind == tokLabel {
			label := tok.val
			if p.loopLabels[label] {
				return nil, p.errorf(tok.pos, "duplicate loop label %q", label)
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, err
			}
			kw, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			labelComment := p.docComment
			switch kw.val {
			case "loop":
				loopStmt, err := p.parseLoopStmt(p.fnParseCtx(ctx), labelComment, label)
				if err != nil {
					return nil, err
				}
				astBody = append(astBody, loopStmt)
			case "while":
				whileStmt, err := p.parseWhileStmt(p.fnParseCtx(ctx), labelComment, label)
				if err != nil {
					return nil, err
				}
				astBody = append(astBody, whileStmt)
			case "for":
				forStmt, err := p.parseForStmt(p.fnParseCtx(ctx), labelComment, label)
				if err != nil {
					return nil, err
				}
				astBody = append(astBody, forStmt)
			default:
				return nil, p.errorf(kw.pos, "expected 'loop', 'while', or 'for' after label, got %s", kw.describe())
			}
			continue
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
			var blocks []*ContinuationBlock
			if hasLocalExecBindings(frame) {
				blocks, err = p.maybeParseFnBodyLocalBlocks(frame, ctx)
				if err != nil {
					return nil, err
				}
			}
			astBody = append(astBody, &InstructionStmt{Frame: frame, Blocks: blocks, Comment: comment})

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
				var blocks []*ContinuationBlock
				if hasLocalExecBindings(frame) {
					blocks, err = p.maybeParseFnBodyLocalBlocksExpr(frame, ctx)
					if err != nil {
						return nil, err
					}
				}
				astBody = append(astBody, &ReturnStmt{Values: []Expr{&InstructionExpr{Frame: frame, Blocks: blocks}}, Comment: comment})
			} else if retPeek.kind == tokIdent && ctx.isExecName(retPeek.val) {
				// Continuation dispatch: return <cont_name> or return <cont_name>(args...)
				contName := retPeek.val
				var contArgs []Expr
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokLParen {
					// Check for empty arg list: return cont()
					emptyCheck, err := p.next()
					if err != nil {
						return nil, err
					}
					if emptyCheck.kind == tokRParen {
						return nil, p.errorf(peek.pos, "empty continuation arg list; use 'return %s' without parentheses for control-only dispatch", contName)
					}
					p.unget(emptyCheck)
					// Parse data args (full expression language)
					for {
						arg, err := p.parseFnBodyReturnItem(ctx)
						if err != nil {
							return nil, err
						}
						contArgs = append(contArgs, arg)
						sep, err := p.next()
						if err != nil {
							return nil, err
						}
						if sep.kind == tokRParen {
							break
						}
						if sep.kind != tokComma {
							return nil, p.errorf(sep.pos, "expected ',' or ')' in continuation args, got %v", sep.kind)
						}
					}
				} else {
					p.unget(peek)
				}
				astBody = append(astBody, &ReturnStmt{Continuation: contName, ContinuationArgs: contArgs, Comment: comment})
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
				ifExpr, err := p.parseIfExpr(p.fnParseCtx(ctx), comment)
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
			stmt, err := p.parseIfStmt(p.fnParseCtx(ctx), comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "while":
			stmt, err := p.parseWhileStmt(p.fnParseCtx(ctx), comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "loop":
			stmt, err := p.parseLoopStmt(p.fnParseCtx(ctx), comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "for":
			stmt, err := p.parseForStmt(p.fnParseCtx(ctx), comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "yield":
			if !ctx.inIter {
				return nil, p.errorf(tok.pos, "'yield' can only be used inside an iter body")
			}
			stmt, err := p.parseYieldStmt(ctx, comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "wait":
			stmt, err := p.parseWaitStmt(p.fnParseCtx(ctx), comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "break":
			if p.loopDepth == 0 && p.execBlockDepth == 0 {
				return nil, p.errorf(tok.pos, "'break' outside of loop or exec block")
			}
			label := ""
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokLabel {
				if !p.loopLabels[peek.val] {
					return nil, p.errorf(peek.pos, "unknown loop label %q", peek.val)
				}
				label = peek.val
			} else {
				p.unget(peek)
			}
			astBody = append(astBody, &BreakStmt{Label: label, Comment: comment})

		case "exit":
			astBody = append(astBody, &ExitStmt{Comment: comment})

		case "restart":
			astBody = append(astBody, &RestartStmt{Comment: comment})

		case "jump":
			stmt, err := p.parseJumpStmt(p.fnParseCtx(ctx), comment)
			if err != nil {
				return nil, err
			}
			astBody = append(astBody, stmt)

		case "last":
			astBody = append(astBody, &LastStmt{Comment: comment})

		case "fn", "iter", "private":
			return nil, p.errorf(tok.pos, "function definitions cannot be nested")

		case "behavior":
			return nil, p.errorf(tok.pos, "behavior definitions cannot be nested")

		case "else":
			return nil, p.errorf(tok.pos, "'else' without matching 'if'")

		case "continue":
			if p.loopDepth == 0 {
				return nil, p.errorf(tok.pos, "'continue' outside of loop")
			}
			astBody = append(astBody, &ContinueStmt{Comment: comment})

		default:
			// Check for assignment, compound assignment, ++/--, or bare call
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokEquals || isCompoundAssignOp(peek.kind) || peek.kind == tokPlusPlus || peek.kind == tokMinusMinus {
				if err := p.checkVarName(tok.val, tok.pos); err != nil {
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
					if callee != nil && (callee.hasReturn() || callee.hasExec()) {
						args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, ctx)
						if err != nil {
							return nil, err
						}
						callExpr := &CallExpr{Name: calleeName, Args: args, KwArgs: kwArgs}
						if callee.hasExec() {
							blocks, err := p.maybeParseFnBodyContinuationBlocksExpr(callee, ctx)
							if err != nil {
								return nil, err
							}
							if blocks != nil {
								callExpr.Blocks = blocks
							}
						}
						result := Expr(callExpr)
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

				// Check for continuation blocks after call args
				var blocks []*ContinuationBlock
				if callee.hasExec() {
					blocks, err = p.maybeParseFnBodyContinuationBlocks(callee, ctx)
					if err != nil {
						return nil, err
					}
				}

				astBody = append(astBody, &CallStmt{
					Name:    calleeName,
					Args:    args,
					KwArgs:  kwArgs,
					Blocks:  blocks,
					Comment: comment,
				})
			}
		}
		if len(astBody) > 0 {
			if last := astBody[len(astBody)-1]; isTerminalStmt(last) {
				terminal = last
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
		if err := p.checkVarName(varTok.val, varTok.pos); err != nil {
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
				if err := p.checkVarName(nameTok.val, nameTok.pos); err != nil {
					return nil, err
				}
				bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: false, Pos: nameTok.pos})
			case "var":
				activeModifier = 1
				nameTok, err := p.expect(tokIdent)
				if err != nil {
					return nil, err
				}
				if err := p.checkVarName(nameTok.val, nameTok.pos); err != nil {
					return nil, err
				}
				bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: true, Pos: nameTok.pos})
			default:
				if err := p.checkVarName(bindTok.val, bindTok.pos); err != nil {
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
			if err := p.checkFnBodyInstructionDirections(frame, ctx.paramDirs, firstTok.pos); err != nil {
				return nil, err
			}
			var blocks []*ContinuationBlock
			if hasLocalExecBindings(frame) {
				blocks, err = p.maybeParseFnBodyLocalBlocksExpr(frame, ctx)
				if err != nil {
					return nil, err
				}
			}
			retCount := frameReturnCount(frame)
			if blocks == nil {
				if retCount == 0 {
					return nil, p.errorf(firstTok.pos, "instruction has no return slots (@N); cannot assign its result")
				}
				if len(bindings) > retCount {
					return nil, p.errorf(firstTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
				}
			}
			for _, bind := range bindings {
				if !bind.Discard {
					ctx.declareFnVarWarn(bind.Name, bind.Mutable, p, bind.Pos)
				}
			}
			return []Stmt{&MultiReturnStmt{
				Bindings: bindings,
				Value:    &InstructionExpr{Frame: frame, Blocks: blocks},
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
				mbe, err := p.parseModeBlockExpr(tok.val == "unlocked", p.fnParseCtx(ctx), comment)
				if err != nil {
					return nil, err
				}
				items = append(items, mbe)
				bindingsConsumed += p.exprArity(mbe.Tail)
				continue
			}

			if tok.kind == tokIdent && tok.val == "if" {
				ifExpr, err := p.parseIfExpr(p.fnParseCtx(ctx), comment)
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
		mbe, err := p.parseModeBlockExpr(rhsTok.val == "unlocked", p.fnParseCtx(ctx), "")
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
		ifExpr, err := p.parseIfExpr(p.fnParseCtx(ctx), "")
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
		var blocks []*ContinuationBlock
		if hasLocalExecBindings(frame) {
			blocks, err = p.maybeParseFnBodyLocalBlocksExpr(frame, ctx)
			if err != nil {
				return nil, err
			}
		}
		if blocks == nil && !frameHasReturnSlot(frame) {
			return nil, p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		return &InstructionExpr{Frame: frame, Blocks: blocks}, nil
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
			if !callee.hasReturn() && !callee.hasExec() {
				return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsName)
			}
			args, kwArgs, err := p.parseFnBodyCallArgs(callee, token{kind: tokIdent, val: rhsName, pos: rhsTok.pos}, ctx)
			if err != nil {
				return nil, err
			}
			callExpr := &CallExpr{Name: rhsName, Args: args, KwArgs: kwArgs}
			if callee.hasExec() {
				blocks, err := p.maybeParseFnBodyContinuationBlocksExpr(callee, ctx)
				if err != nil {
					return nil, err
				}
				if blocks != nil {
					callExpr.Blocks = blocks
				}
			}
			return callExpr, nil
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
	// Skip optional clauses between params and body:
	//   fn: exec(name, name)
	//   iter: -> name, name
	tok, err := p.next()
	if err != nil {
		return err
	}
	if tok.kind == tokIdent && tok.val == "exec" {
		if _, err := p.expect(tokLParen); err != nil {
			return err
		}
		for {
			t, err := p.next()
			if err != nil {
				return err
			}
			if t.kind == tokRParen {
				break
			}
		}
	} else if tok.kind == tokArrow {
		// iter output names: -> name, name, ...
		// Skip identifiers and commas until we hit '{'
		for {
			t, err := p.next()
			if err != nil {
				return err
			}
			if t.kind == tokLBrace {
				p.unget(t)
				break
			}
		}
	} else {
		p.unget(tok)
	}
	return p.skipBraceBlock()
}

func (p *parser) parseInstruction() (map[string]any, error) {
	opTok, err := p.expect(tokString)
	if err != nil {
		return nil, err
	}

	// Block is optional — `instruction "nop"` is valid when no fields are needed.
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokLBrace {
		p.unget(tok)
		return map[string]any{"op": opTok.val}, nil
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

		// Check for exec binding modifiers: detach, exec, next
		detached := false
		isExec := false

		// 'detach' modifier implies exec and detached
		if tok.kind == tokIdent && tok.val == "detach" {
			detached = true
			isExec = true
			tok, err = p.next()
			if err != nil {
				return nil, err
			}
		}

		// 'exec' keyword marks an exec binding (redundant after 'detach')
		if tok.kind == tokIdent && tok.val == "exec" {
			isExec = true
			tok, err = p.next()
			if err != nil {
				return nil, err
			}
		}

		// 'next' as key implies exec
		if tok.kind == tokIdent && tok.val == "next" && !isExec {
			isExec = true
		}

		if tok.kind != tokIdent && tok.kind != tokNumber {
			return nil, p.errorf(tok.pos, "expected field name or '}', got %s", tok.describe())
		}
		key := tok.val
		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}

		if isExec {
			// Parse exec binding value: continuation name + optional args
			valTok, err := p.next()
			if err != nil {
				return nil, err
			}
			var eb execBinding
			if valTok.kind == tokLabel {
				// 'name — local block binding
				if valTok.val == "return" {
					return nil, p.errorf(valTok.pos, "'return is reserved and cannot be used as a local block name")
				}
				eb = execBinding{name: valTok.val, detached: detached, local: true}
			} else if valTok.kind == tokIdent {
				// Allow "return" as a special continuation name
				if valTok.val != "return" && Keywords[valTok.val] {
					return nil, p.errorf(valTok.pos, "expected continuation name, got keyword %q", valTok.val)
				}
				eb = execBinding{name: valTok.val, detached: detached}
			} else {
				return nil, p.errorf(valTok.pos, "expected continuation name or 'label, got %s", valTok.describe())
			}

			// Parse optional argument list: name(@1, @2) or return(@1)
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokLParen {
				eb.args, err = p.parseExecBindingArgs()
				if err != nil {
					return nil, err
				}
			} else {
				p.unget(peek)
			}

			frame[key] = eb
		} else {
			valTok, err := p.next()
			if err != nil {
				return nil, err
			}
			switch valTok.kind {
			case tokString, tokIdent:
				frame[key] = valTok.val
			case tokNumber:
				n, _ := strconv.Atoi(valTok.val)
				frame[key] = n
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
				return nil, p.errorf(valTok.pos, "expected string, identifier, number, or @N, got %s", valTok.describe())
			}
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

// expandCallOpts holds optional parameters for expandCall.
type expandCallOpts struct {
	blocks        []*ContinuationBlock
	emitBlockBody func(stmts []Stmt, bindings map[string]any) error // emitter for block bodies; bindings map param names to register values
	emitTail      func(Expr) error                                  // emitter for expression-form block tails; nil = no tails
}

func (p *parser) expandCall(name string, args []any, kwArgs map[string]any, retVals []any, b *frameBuilder, pos int, comment string, usedVars map[string]bool, opts ...expandCallOpts) error {
	fn := p.fns[name]
	if fn == nil {
		return p.errorf(pos, "unknown function %q", name)
	}

	// Extract optional parameters
	var contBlocks []*ContinuationBlock
	var emitBlockBody func([]Stmt, map[string]any) error
	var emitTailFn func(Expr) error
	if len(opts) > 0 {
		contBlocks = opts[0].blocks
		emitBlockBody = opts[0].emitBlockBody
		emitTailFn = opts[0].emitTail
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

	if fn.frame != nil && contBlocks == nil {
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

	// For branching functions with continuation data, allocate output
	// registers for @N slots so resolveInstructionFrame assigns them.
	// Reuses registers already assigned via fn.rets + retVals when
	// possible, allocates new ones using block param names otherwise.
	var execOutputRegs map[int]string // returnSlot(N) → register name
	if contBlocks != nil && fn.hasExec() {
		maxSlot := findMaxExecOutputSlot(fn.astBody)
		// Also consider pure-logic continuation args
		for _, count := range fn.execContArgs {
			if count > maxSlot {
				maxSlot = count
			}
		}
		if maxSlot > 0 {
			execOutputRegs = allocExecOutputRegs(fn, contBlocks, maxSlot, paramMap, usedVars)
		}
		// Add synthetic @carg names to paramMap for pure-logic continuation args
		if fn.execContArgs != nil && execOutputRegs != nil {
			for i := 1; i <= maxSlot; i++ {
				if reg, ok := execOutputRegs[i]; ok {
					paramMap["@carg"+strconv.Itoa(i)] = reg
				}
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

	// Handle continuation blocks (branching expansion)
	if contBlocks != nil && fn.hasExec() {
		bodyEmitter := emitBlockBody
		if bodyEmitter == nil {
			// Default: use emitFnBody with empty paramMap (fn body level)
			bodyEmitter = func(stmts []Stmt, bindings map[string]any) error {
				pm := maps.Clone(paramMap)
				for k, v := range bindings {
					pm[k] = v
				}
				return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
			}
		}
		if err := p.expandContinuationBlocks(fn, contBlocks, b, origPos, bodyEmitter, execOutputRegs, emitTailFn); err != nil {
			return err
		}
		return nil
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
