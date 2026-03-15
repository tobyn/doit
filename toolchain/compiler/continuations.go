package compiler

import (
	"fmt"
	"strconv"
	"strings"
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
//
//	name1 { body } name2 { body } ...
//
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
