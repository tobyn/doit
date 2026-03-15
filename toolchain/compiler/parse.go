package compiler

import (
	"fmt"
	"maps"
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
		return nil, p.errorf(firstTok.pos, "empty continuation block")
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
func (p *parser) parseExecBindingArgs(openPos int) ([]any, error) {
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
		return nil, p.errorf(openPos, "empty exec binding argument list")
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
func (p *parser) expandContinuationBlocks(fn *fnDef, blocks []*ContinuationBlock, b *frameBuilder, origPos int, emitBody func([]Stmt, map[string]any) error, execOutputRegs map[int]string, emitTail func(Expr) error, breakRetVals []any) error {
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

		// Set breakRetVals for expression-form blocks so break-with-value
		// inside the body can write to the same target registers as the tail.
		savedBreakRetVals := p.breakRetVals
		if breakRetVals != nil && blk.Tail != nil && !blk.Detached {
			p.breakRetVals = breakRetVals
		}

		if err := emitBody(blk.Body, bindings); err != nil {
			p.breakRetVals = savedBreakRetVals
			return err
		}
		p.breakRetVals = savedBreakRetVals

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
						"0":    false,
						"1":    false,
						"next": false,
					}
				}
			}
		} else {
			// Bridging block: jump to join point after body
			jumpIdx := b.emit(map[string]any{
				"op": "set_reg",
				"0":  false,
				"1":  false,
				// "next" will be patched to join point
			})
			bridgeJumps = append(bridgeJumps, jumpIdx)
			// Patch @break to jump to join point
			for j := blockStarts[blk.Name]; j < b.pos(); j++ {
				if op, _ := b.frames[j]["op"].(string); op == "@break" {
					b.frames[j] = map[string]any{
						"op": "set_reg",
						"0":  false,
						"1":  false,
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
			} else if eb.detached {
				// Unprovided detached continuation → remove from frame.
				// The game sees absent exec slots as unconnected and skips
				// them (e.g., sequence's optional Second–Fourth steps).
				delete(f, k)
			} else {
				// Unprovided bridging continuation → bridge to join point
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
				"0":    false,
				"1":    false,
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
						"0":    false,
						"1":    false,
						"next": false,
					}
				}
			}
		} else {
			// Bridging block: jump to join point after body
			jumpIdx := b.emit(map[string]any{
				"op": "set_reg",
				"0":  false,
				"1":  false,
			})
			bridgeJumps = append(bridgeJumps, jumpIdx)
			// Patch @break to jump to join point
			for j := blockStarts[blk.Name]; j < b.pos(); j++ {
				if op, _ := b.frames[j]["op"].(string); op == "@break" {
					b.frames[j] = map[string]any{
						"op": "set_reg",
						"0":  false,
						"1":  false,
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

	// Build direction and param flag maps for enforcement in body
	paramDirs := map[string]string{}
	paramFlags := map[string]bool{}
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
		if pd.isParam {
			paramFlags[pd.name] = true
		}
	}
	ctx := &fnBodyContext{
		paramDirs:   paramDirs,
		paramFlags:  paramFlags,
		fnVarInfo:   map[string]fnVarInfo{},
		inIter:      true,
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
			return p.buildInstructionIter(nameTok.val, nameTok.pos, params, outputs, instrStmt.Frame, private)
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
func (p *parser) buildInstructionIter(name string, pos int, params []paramDef, outputs []string, frame map[string]any, private bool) (string, error) {
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
				return "", p.errorf(pos, "iter %q: 'done' value must be a number", name)
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
		return "", p.errorf(pos, "iter %q: instruction block requires 'done: N' to specify exhaustion slot", name)
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
func (p *parser) parseYieldStmt(ctx *fnBodyContext, pos int, comment string) (*YieldStmt, error) {
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
		return nil, p.errorf(pos, "yield produces %d value(s), but iter declares %d output(s)", len(values), len(ctx.iterOutputs))
	}

	return &YieldStmt{Pos: pos, Values: values, Comment: comment}, nil
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
		return p.errorf(pos, "unknown function or variable %q%s", e.Name, suggest(e.Name, collectKeys(p.fns)))
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
	case *DotAccessExpr:
		return p.checkFnBodyExprDeclared(e.Value, ctx, pos)
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
		if strings.HasPrefix(tok.val, "%") {
			return &LiteralExpr{Value: map[string]any{"fr": tok.val[1:]}}, nil
		}
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
			return nil, p.errorf(tok.pos, "unknown function or variable %q%s", tok.val, suggest(tok.val, collectKeys(p.fns)))
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
		} else if innerTok.kind == tokIdent && !isConstructor(innerTok.val) && innerTok.val != "null" && innerTok.val != "false" && innerTok.val != "true" && innerTok.val != "infinity" && innerTok.val != "not_equal" {
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
		} else if tok.val == "infinity" {
			base = &LiteralExpr{Value: map[string]any{"num": -2147483648}}
		} else if tok.val == "not_equal" {
			base = &LiteralExpr{Value: map[string]any{"num": -2147483647}}
		} else if isConstructor(tok.val) {
			ctor, err := p.parseFnBodyConstructorExpr(tok)
			if err != nil {
				return nil, err
			}
			base = ctor
		} else if strings.HasPrefix(tok.val, "%") {
			base = &LiteralExpr{Value: map[string]any{"fr": tok.val[1:]}}
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

	if err := p.checkFnBodyCallDirectionsExpr(callee, calleeTok.val, args, kwArgs, paramDirs, letVars, calleeTok.pos, ctx.paramFlags, ctx.behaviorFlags); err != nil {
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
func (p *parser) checkFnBodyCallDirectionsExpr(callee *fnDef, calleeName string, args []Expr, kwArgs map[string]Expr, paramDirs map[string]string, letVars map[string]fnVarInfo, pos int, pFlags map[string]bool, bFlags map[string]bool) error {
	posIdx := 0
	for _, pd := range callee.params {
		calleeDir := pd.effectiveDirection()
		var argExpr Expr
		if pd.keyword == "" {
			if posIdx < len(args) {
				argExpr = args[posIdx]
				aDir := fnBodyExprDir(argExpr, paramDirs, letVars)
				if !canPass(calleeDir, aDir) {
					return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
						aDir, calleeDir, pd.name, calleeName)
				}
			}
			posIdx++
		} else if kwArgs != nil {
			if val, ok := kwArgs[pd.keyword]; ok {
				argExpr = val
				aDir := fnBodyExprDir(val, paramDirs, letVars)
				if !canPass(calleeDir, aDir) {
					return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
						aDir, calleeDir, pd.name, calleeName)
				}
			}
		}
		if argExpr == nil {
			continue
		}
		// Check param modifier: argument must be a param-flagged parameter
		if pd.isParam {
			if ident, ok := argExpr.(*IdentExpr); ok {
				if pFlags != nil && pFlags[ident.Name] {
					continue // transitively param
				}
			}
			return p.errorf(pos, "argument to param parameter %q of %s must be a behavior parameter",
				pd.name, calleeName)
		}
		// Check behavior modifier: argument must be a behavior-flagged parameter
		if pd.isBehavior {
			if ident, ok := argExpr.(*IdentExpr); ok {
				if bFlags != nil && bFlags[ident.Name] {
					continue // transitively behavior
				}
			}
			return p.errorf(pos, "argument to behavior parameter %q of %s must be a behavior reference",
				pd.name, calleeName)
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

		// Check for param or behavior modifier
		isParamMod := false
		isBehaviorMod := false
		if tok.val == "param" {
			isParamMod = true
			tok, err = p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
		} else if tok.val == "behavior" {
			isBehaviorMod = true
			if direction == "out" || direction == "inout" {
				return nil, p.errorf(tok.pos, "behavior parameter cannot be %s", direction)
			}
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
				name: peek.val, keyword: tok.val, direction: direction, isParam: isParamMod, isBehavior: isBehaviorMod,
			})
		} else {
			// positional param
			p.unget(peek)
			if seenKeyword {
				return nil, p.errorf(tok.pos, "positional parameter after keyword parameter")
			}
			params = append(params, paramDef{name: tok.val, direction: direction, isParam: isParamMod, isBehavior: isBehaviorMod})
		}
	}
	return params, nil
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
// ("function", "constant", or "enum"). For functions, imported names are
// excluded (user functions may override imports).
func (p *parser) checkDeclName(name, kind string, pos int) error {
	if _, ok := p.fns[name]; ok {
		if kind == "function" {
			// Functions can override imports; only duplicate
			// same-file user fns are errors.
			if !p.importedNames[name] {
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
				// Initialize dependency tracking for call keyword
				if p.depIndex == nil {
					p.dependencies = nil
					p.depIndex = map[string]int{}
					p.depCompiling = map[string]bool{}
					p.selfBehaviorID = p.target
				}
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

// scanBehaviorParams scans @param declarations from a behavior body to extract
// parameter metadata. Consumes the opening { and everything up to the matching }.
// Returns the parameter definitions without building a full symbol table.
func (p *parser) scanBehaviorParams() ([]paramDef, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	var params []paramDef
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			return params, nil
		}
		if tok.kind == tokEOF {
			return nil, p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind != tokAt {
			// Not an attribute — done scanning params, skip the rest
			p.unget(tok)
			if err := p.skipToCloseBrace(); err != nil {
				return nil, err
			}
			return params, nil
		}
		attr, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		if attr.val != "param" {
			// Skip non-param attributes (@name, @keepvars, @keeparrays)
			// by consuming tokens until the next @ or non-attribute token
			for {
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokAt || peek.kind == tokRBrace || peek.kind == tokEOF {
					p.unget(peek)
					break
				}
				if peek.kind == tokLBrace {
					// Nested brace block (localize, etc.) — skip it entirely
					if err := p.skipToCloseBrace(); err != nil {
						return nil, err
					}
					continue
				}
				// Also stop at identifiers that could be statements
				if peek.kind == tokIdent {
					if Keywords[peek.val] && peek.val != "localize" && peek.val != "in" && peek.val != "out" && peek.val != "inout" && peek.val != "true" && peek.val != "false" {
						p.unget(peek)
						break
					}
				}
			}
			continue
		}
		// Parse @param direction name ["display"] [= default]
		dirTok, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		switch dirTok.val {
		case "in", "out", "inout":
			// valid
		default:
			return nil, p.errorf(dirTok.pos, "expected parameter direction (in, out, inout), got %q", dirTok.val)
		}
		nameTok, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		params = append(params, paramDef{
			name:      nameTok.val,
			keyword:   nameTok.val,
			direction: dirTok.val,
		})
		// Skip optional display name and default value
		for {
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokAt || peek.kind == tokRBrace || peek.kind == tokEOF {
				p.unget(peek)
				break
			}
			if peek.kind == tokIdent && peek.val == "localize" {
				// Skip the localize block
				if _, err := p.expect(tokLBrace); err != nil {
					return nil, err
				}
				if err := p.skipToCloseBrace(); err != nil {
					return nil, err
				}
				continue
			}
			if peek.kind == tokIdent {
				if Keywords[peek.val] && peek.val != "true" && peek.val != "false" && peek.val != "in" && peek.val != "out" && peek.val != "inout" {
					p.unget(peek)
					break
				}
			}
		}
	}
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
			params, err := p.scanBehaviorParams()
			if err != nil {
				return err
			}
			if p.bhvs == nil {
				p.bhvs = map[string]*bhvDef{}
			}
			p.bhvs[idTok.val] = &bhvDef{
				params:     params,
				sourceFS:   p.sourceFS,
				sourcePath: p.sourcePath,
				sourceText: p.src,
				prelude:    p.prelude,
			}
			if isImport {
				p.fileDecls = append(p.fileDecls, idTok.val)
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
	paramDirs     map[string]string    // param name -> effective direction
	paramFlags    map[string]bool      // param name -> true if param modifier (requires behavior parameter)
	behaviorFlags map[string]bool      // param name -> true if behavior modifier (requires behavior reference)
	fnVarInfo     map[string]fnVarInfo // name -> var info (mutability, depth, used tracking)
	fnScopeDepth  int                  // current nesting depth (0 = fn top-level)
	resolve       operandResolver
	execNames     []string // continuation names from exec(...) declaration (nil if none)
	inIter        bool     // true when parsing an iter body
	iterOutputs   []string // output names from iter -> declaration
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
	if strings.HasPrefix(name, "%") {
		return nil
	}
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
	return p.errorf(pos, "undeclared variable %q%s", name, suggest(name, collectKeys(ctx.fnVarInfo)))
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

	// Build direction and param flag maps for enforcement in fn body
	paramDirs := map[string]string{}    // param name -> effective direction
	paramFlags := map[string]bool{}     // param name -> true if param modifier
	behaviorFlags := map[string]bool{}  // param name -> true if behavior modifier
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
		if pd.isParam {
			paramFlags[pd.name] = true
		}
		if pd.isBehavior {
			behaviorFlags[pd.name] = true
		}
	}
	ctx := &fnBodyContext{
		paramDirs:     paramDirs,
		paramFlags:    paramFlags,
		behaviorFlags: behaviorFlags,
		fnVarInfo:     map[string]fnVarInfo{},
		execNames:     execNames,
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
				return "", p.errorf(ret.Pos, "inconsistent arg count for continuation %q: %d vs %d", ret.Continuation, prev, count)
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
		defer p.enterExecBlock()()
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
		defer p.enterExecBlock()()
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
		defer p.enterExecBlock()()
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
		defer p.enterExecBlock()()
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

// parseFnBodyOnEvent parses an `on` event handler inside a function body.
// Syntax: `on paramName { body }` or `on radio(bandExpr) -> signal { body }`
func (p *parser) parseFnBodyOnEvent(ctx *fnBodyContext, comment string) (*OnEventStmt, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}

	// radio event: on radio(bandExpr) -> signal { body }
	if tok.kind == tokIdent && tok.val == "radio" {
		if _, err := p.expect(tokLParen); err != nil {
			return nil, err
		}
		bandExpr, err := p.parseArithExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		signal := ""
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokArrow {
			sigTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if Keywords[sigTok.val] {
				return nil, p.errorf(sigTok.pos, "expected signal variable name, got keyword %q", sigTok.val)
			}
			signal = sigTok.val
			ctx.declareFnVar(signal, false)
		} else {
			p.unget(peek)
		}
		if _, err := p.expect(tokLBrace); err != nil {
			return nil, err
		}
		body, err := p.parseFnBodyStmts(ctx)
		if err != nil {
			return nil, err
		}
		return &OnEventStmt{
			Kind:    "radio",
			Band:    bandExpr,
			Signal:  signal,
			Body:    body,
			Comment: comment,
		}, nil
	}

	// parameter event: on paramName { body }
	if tok.kind != tokIdent {
		return nil, p.errorf(tok.pos, "expected parameter name or 'radio' after 'on', got %s", tok.describe())
	}
	paramName := tok.val
	if !ctx.paramFlags[paramName] {
		if _, isParam := ctx.paramDirs[paramName]; !isParam {
			return nil, p.errorf(tok.pos, "unknown parameter %q in event handler", paramName)
		}
		return nil, p.errorf(tok.pos, "event listener requires a param argument; %q is not declared with the param modifier", paramName)
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseFnBodyStmts(ctx)
	if err != nil {
		return nil, err
	}
	return &OnEventStmt{
		Kind:    "parameter",
		Param:   paramName,
		Body:    body,
		Comment: comment,
	}, nil
}

// parseFnBodyStmts parses fn body statements until '}'. The opening '{'
// has been consumed. Delegates to the unified parseStmtBlock.
func (p *parser) parseFnBodyStmts(ctx *fnBodyContext) ([]Stmt, error) {
	return p.parseStmtBlock(p.fnParseCtx(ctx), false)
}

// parseFnBodyStmtsInner parses fn body statements until '}'. If exprTail is
// true, the last item may be a bare expression (wrapped in exprTailStmt).
// Delegates to the unified parseStmtBlock.
func (p *parser) parseFnBodyStmtsInner(ctx *fnBodyContext, exprTail bool) ([]Stmt, error) {
	return p.parseStmtBlock(p.fnParseCtx(ctx), exprTail)
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

	// Behavior call expression RHS: let x = call foo(param: 5)
	if rhsTok.kind == tokIdent && rhsTok.val == "call" {
		callNameTok, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		name, bhv, err := p.resolveCallBehaviorName(callNameTok)
		if err != nil {
			return nil, err
		}
		args, err := p.parseCallBehaviorArgs(bhv, ctx.resolve, callNameTok.pos)
		if err != nil {
			return nil, err
		}
		return &CallBehaviorExpr{BehaviorName: name, Args: args, Pos: callNameTok.pos}, nil
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

// parseFnDefaultStmtUnified handles the default case (non-keyword identifier)
// in unified statement parsing for fn body context. When exprTail is true,
// tries expression-tail forms first; falls through to regular statement parsing
// otherwise. Returns (stmts, done, err) where done=true means exprTail consumed '}'.
func (p *parser) parseFnDefaultStmtUnified(tok token, ctx *fnBodyContext, comment string, exprTail bool) ([]Stmt, bool, error) {
	// Handle _ (discard) — treat as call without capturing result
	if tok.val == "_" {
		sep, err := p.next()
		if err != nil {
			return nil, false, err
		}
		if sep.kind == tokEquals {
			expr, err := p.parseFnBodyRHSExpr(ctx)
			if err != nil {
				return nil, false, err
			}
			// Wrap in a CallStmt if it's a call, otherwise AssignStmt with discard
			if ce, ok := expr.(*CallExpr); ok {
				return []Stmt{&CallStmt{Name: ce.Name, Args: ce.Args, KwArgs: ce.KwArgs, Blocks: ce.Blocks, Comment: comment}}, false, nil
			}
			// For non-call expressions, just evaluate and discard
			return []Stmt{&AssignStmt{Target: "_", Value: expr, Comment: comment, Pos: tok.pos}}, false, nil
		}
		if sep.kind == tokComma {
			// Multi-return with leading discard: _, b, c = fn args
			p.unget(sep)
			p.unget(tok)
			// Re-parse as let _, ...
			stmts, err := p.parseFnBodyLetVar(ctx, false, comment)
			return stmts, false, err
		}
		return nil, false, p.errorf(sep.pos, "expected ',' or '=' after '_', got %s", sep.describe())
	}

	// Check for assignment, compound assignment, ++/--, or bare call
	peek, err := p.next()
	if err != nil {
		return nil, false, err
	}
	if peek.kind == tokEquals || isCompoundAssignOp(peek.kind) || peek.kind == tokPlusPlus || peek.kind == tokMinusMinus {
		if err := p.checkVarName(tok.val, tok.pos); err != nil {
			return nil, false, err
		}
	}
	if peek.kind == tokEquals {
		if err := ctx.canAssign(tok.val, p, tok.pos); err != nil {
			return nil, false, err
		}
		expr, err := p.parseFnBodyRHSExpr(ctx)
		if err != nil {
			return nil, false, err
		}
		return []Stmt{&AssignStmt{Target: tok.val, Value: expr, Comment: comment, Pos: tok.pos}}, false, nil
	} else if isCompoundAssignOp(peek.kind) {
		if err := ctx.canCompound(tok.val, p, tok.pos); err != nil {
			return nil, false, err
		}
		rhs, err := p.parseBoolExpr(ctx.resolve)
		if err != nil {
			return nil, false, err
		}
		if truthy, ok := rhs.(*TruthyExpr); ok {
			rhs = truthy.Value
		}
		return []Stmt{&CompoundAssignStmt{Target: tok.val, Op: peek.kind, Value: rhs, Comment: comment, Pos: tok.pos}}, false, nil
	} else if peek.kind == tokPlusPlus {
		if err := ctx.canCompound(tok.val, p, tok.pos); err != nil {
			return nil, false, err
		}
		return []Stmt{&IncrDecrStmt{Target: tok.val, Op: tokPlusPlus, Comment: comment, Pos: tok.pos}}, false, nil
	} else if peek.kind == tokMinusMinus {
		if err := ctx.canCompound(tok.val, p, tok.pos); err != nil {
			return nil, false, err
		}
		return []Stmt{&IncrDecrStmt{Target: tok.val, Op: tokMinusMinus, Comment: comment, Pos: tok.pos}}, false, nil
	}

	p.unget(peek)
	calleeName, callee, calleeErr := p.resolveFnName(tok)
	if calleeErr != nil {
		return nil, false, calleeErr
	}
	calleeTok := token{kind: tokIdent, val: calleeName, pos: tok.pos}

	if exprTail {
		if isConstructor(tok.val) {
			ctor, err := p.parseFnBodyConstructorExpr(tok)
			if err != nil {
				return nil, false, err
			}
			peek2, err := p.next()
			if err != nil {
				return nil, false, err
			}
			var tailExpr Expr = ctor
			if peek2.kind == tokAmpersand {
				if ctorExpr, ok := ctor.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
					return nil, false, p.errorf(peek2.pos, "'&' cannot be used with Range (it would overwrite the step field)")
				}
				numExpr, err := p.parseFnBodyExpr()
				if err != nil {
					return nil, false, err
				}
				tailExpr = &AmpersandExpr{Value: ctor, Num: numExpr}
			} else {
				p.unget(peek2)
			}
			if _, err := p.expect(tokRBrace); err != nil {
				return nil, false, err
			}
			return []Stmt{&exprTailStmt{Expr: tailExpr}}, true, nil
		}
		if tok.val == "null" || tok.val == "false" {
			if _, err := p.expect(tokRBrace); err != nil {
				return nil, false, err
			}
			return []Stmt{&exprTailStmt{Expr: &LiteralExpr{Value: false}}}, true, nil
		}
		if tok.val == "true" || tok.val == "infinity" || tok.val == "not_equal" {
			litVal := map[string]any{"num": 1}
			if tok.val == "infinity" {
				litVal = map[string]any{"num": -2147483648}
			} else if tok.val == "not_equal" {
				litVal = map[string]any{"num": -2147483647}
			}
			if _, err := p.expect(tokRBrace); err != nil {
				return nil, false, err
			}
			return []Stmt{&exprTailStmt{Expr: &LiteralExpr{Value: litVal}}}, true, nil
		}
		if callee != nil && (callee.hasReturn() || callee.hasExec()) {
			args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, ctx)
			if err != nil {
				return nil, false, err
			}
			callExpr := &CallExpr{Name: calleeName, Args: args, KwArgs: kwArgs}
			if callee.hasExec() {
				blocks, err := p.maybeParseFnBodyContinuationBlocksExpr(callee, ctx)
				if err != nil {
					return nil, false, err
				}
				if blocks != nil {
					callExpr.Blocks = blocks
				}
			}
			result := Expr(callExpr)
			result, err = p.parseArithExprFromFull(result, ctx.resolve)
			if err != nil {
				return nil, false, err
			}
			final, handled, err := p.maybeExprContinuation(result, ctx.resolve)
			if err != nil {
				return nil, false, err
			}
			if handled {
				result = final
			}
			if _, err := p.expect(tokRBrace); err != nil {
				return nil, false, err
			}
			return []Stmt{&exprTailStmt{Expr: result}}, true, nil
		}
		if callee == nil {
			resolved, err := ctx.resolve(tok)
			if err != nil {
				return nil, false, err
			}
			result, err := p.parseArithExprFromFull(resolved, ctx.resolve)
			if err != nil {
				return nil, false, err
			}
			final, handled, err := p.maybeExprContinuation(result, ctx.resolve)
			if err != nil {
				return nil, false, err
			}
			if handled {
				result = final
			}
			if _, err := p.expect(tokRBrace); err != nil {
				return nil, false, err
			}
			return []Stmt{&exprTailStmt{Expr: result}}, true, nil
		}
	}

	// Bare function call
	if callee == nil {
		return nil, false, p.errorf(tok.pos, "unknown function %q%s", tok.val, suggest(tok.val, collectKeys(p.fns)))
	}
	args, kwArgs, err := p.parseFnBodyCallArgs(callee, calleeTok, ctx)
	if err != nil {
		return nil, false, err
	}
	var blocks []*ContinuationBlock
	if callee.hasExec() {
		blocks, err = p.maybeParseFnBodyContinuationBlocks(callee, ctx)
		if err != nil {
			return nil, false, err
		}
	}
	return []Stmt{&CallStmt{Name: calleeName, Args: args, KwArgs: kwArgs, Blocks: blocks, Comment: comment}}, false, nil
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
				eb.args, err = p.parseExecBindingArgs(peek.pos)
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
	breakRetVals  []any                                             // target registers for break-with-value in expression-form blocks
}

func (p *parser) expandCall(name string, args []any, kwArgs map[string]any, retVals []any, b *frameBuilder, pos int, comment string, usedVars map[string]bool, opts ...expandCallOpts) error {
	fn := p.fns[name]
	if fn == nil {
		return p.errorf(pos, "unknown function %q%s", name, suggest(name, collectKeys(p.fns)))
	}

	// Extract optional parameters
	var contBlocks []*ContinuationBlock
	var emitBlockBody func([]Stmt, map[string]any) error
	var emitTailFn func(Expr) error
	var breakRetValsOpt []any
	if len(opts) > 0 {
		contBlocks = opts[0].blocks
		emitBlockBody = opts[0].emitBlockBody
		emitTailFn = opts[0].emitTail
		breakRetValsOpt = opts[0].breakRetVals
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

	// Temporarily merge the function's scope into p.fns (and p.iters) so
	// that transitive dependencies (functions/iterators called by this fn
	// but not explicitly imported by the caller) are available during body
	// expansion.
	var scopeAdded []string
	if fn.scope != nil {
		for k, v := range fn.scope {
			if _, exists := p.fns[k]; !exists {
				p.fns[k] = v
				scopeAdded = append(scopeAdded, k)
			}
		}
	}
	var iterScopeAdded []string
	if fn.iterScope != nil {
		for k, v := range fn.iterScope {
			if _, exists := p.iters[k]; !exists {
				p.iters[k] = v
				iterScopeAdded = append(iterScopeAdded, k)
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
	for _, k := range iterScopeAdded {
		delete(p.iters, k)
	}

	if err != nil {
		return err
	}

	// Strip unresolved exec bindings and absent keyword params when
	// called without continuation blocks. The AST emission path
	// doesn't pass kwVars to resolveInstructionFrame, so keyword
	// params that weren't provided end up as bare string references.
	if contBlocks == nil && fn.hasExec() {
		kwVars := fn.keywordVarNames()
		for j := origPos; j < b.pos(); j++ {
			for k, v := range b.frames[j] {
				if _, ok := v.(execBinding); ok {
					delete(b.frames[j], k)
				} else if s, ok := v.(string); ok && k != "op" && kwVars[s] {
					if _, inMap := paramMap[s]; !inMap {
						delete(b.frames[j], k)
					}
				}
			}
		}
	}

	for _, rc := range retCopies {
		f := map[string]any{"op": "set_reg", "0": rc.from, "1": rc.to}
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
		if err := p.expandContinuationBlocks(fn, contBlocks, b, origPos, bodyEmitter, execOutputRegs, emitTailFn, breakRetValsOpt); err != nil {
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
				"0":    false,
				"1":    false,
				"next": frameRef(afterAll),
			}
		}
	}
	return nil
}
