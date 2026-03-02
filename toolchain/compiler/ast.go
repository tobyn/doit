package compiler

// --- AST Node Types ---
//
// The AST decouples parsing from emission. Both behavior-level and fn body
// compilation share the same node types, enabling fn bodies to support the
// same constructs as behavior bodies.

// Stmt is a statement node in the AST.
type Stmt interface {
	stmtNode()
}

// Expr is an expression node in the AST.
type Expr interface {
	exprNode()
}

// --- Continuation blocks ---

// ContinuationBlock is a named block of code attached to a branching call.
type ContinuationBlock struct {
	Name    string   // continuation name ("" for collapsed unnamed form)
	Params  []string // Kotlin-style bindings (e.g., "comp, idx ->"); nil = no data
	Body    []Stmt
	Tail    Expr // non-nil for expression-form blocks (value produced by this path)
	Looping bool // true if prefixed with `for` at call site
}

// --- Statement nodes ---

// CallStmt is a bare function call statement: `notify "Hello"`
type CallStmt struct {
	Name    string
	Args    []Expr
	KwArgs  map[string]Expr
	Blocks  []*ContinuationBlock // nil for non-branching calls
	Comment string
}

// LetStmt is a variable declaration with expression RHS: `let x = a + b`
type LetStmt struct {
	Name    string
	Mutable bool // true for var, false for let
	Value   Expr
	Comment string
}

// AssignStmt is an assignment to an existing target: `x = get_self`
type AssignStmt struct {
	Target   string // variable name or $register
	Value    Expr
	Comment  string
	Internal bool // true for compiler-generated assigns (skip mutability check)
	Pos      int  // source position of the target (for diagnostics)
}

// CompoundAssignStmt is a compound assignment: `x += 3`
type CompoundAssignStmt struct {
	Target  string
	Op      tokenKind // tokPlusEquals, tokMinusEquals, etc.
	Value   Expr
	Comment string
	Pos     int // source position of the target (for diagnostics)
}

// IncrDecrStmt is an increment or decrement: `x++`, `x--`
type IncrDecrStmt struct {
	Target  string
	Op      tokenKind // tokPlusPlus or tokMinusMinus
	Comment string
	Pos     int // source position of the target (for diagnostics)
}

// MultiReturnStmt is a multi-return binding: `let x, y = fn args`
type MultiReturnStmt struct {
	Bindings []MultiBinding
	Value    Expr // CallExpr, InstructionExpr, or ExprListExpr
	Comment  string
}

// MultiBinding is a single binding in a multi-return statement.
type MultiBinding struct {
	Name    string // "" for discard
	Discard bool   // true for _
	Mutable bool   // true for var, false for let (only meaningful when !Discard)
	Pos     int    // source position of the binding name (for diagnostics)
}

// InstructionStmt is a bare instruction block: `instruction "op" { ... }`
type InstructionStmt struct {
	Frame   map[string]any
	Comment string
}

// ModeBlockStmt is a locked { ... } or unlocked { ... } block.
type ModeBlockStmt struct {
	Unlock  bool   // false=locked, true=unlocked
	Body    []Stmt
	Comment string
}

// ReturnStmt is a return from a function body: `return x, y`
// When Continuation is non-empty, it's a continuation dispatch: `return big`
type ReturnStmt struct {
	Values       []Expr // nil for bare return or continuation dispatch
	Continuation string // non-empty = dispatch to this exec continuation
	Comment      string
}

// IfStmt is an if/else-if/else block.
type IfStmt struct {
	Cond    Expr
	Body    []Stmt
	ElseIfs []ElseIfClause
	Else    []Stmt // nil if no else
	Comment string
}

// ElseIfClause is an else-if branch.
type ElseIfClause struct {
	Cond Expr
	Body []Stmt
}

// WhileStmt is a while loop.
type WhileStmt struct {
	Label   string // "" for unlabeled
	Cond    Expr
	Body    []Stmt
	Comment string
}

// LoopStmt is a loop. If Count is nil, infinite; otherwise counted.
type LoopStmt struct {
	Label   string // "" for unlabeled
	Count   Expr   // nil = infinite, non-nil = counted
	Body    []Stmt
	Comment string
}

// ForStmt is a for-in loop over a range: `for i in Range(5) { ... }`
type ForStmt struct {
	Label   string // "" for unlabeled
	IterVar string // iteration variable name (let-bound)
	Range   Expr   // range expression
	Body    []Stmt
	Comment string
}

// BreakStmt is a break from a loop.
type BreakStmt struct {
	Label   string // "" for unlabeled (breaks innermost)
	Comment string
}

// ExitStmt terminates the behavior: `exit`
type ExitStmt struct {
	Comment string
}

// WaitStmt is a wait statement: `wait <ticks>` or `wait <ticks> { body; cond }`.
type WaitStmt struct {
	Ticks   Expr   // ticks expression (evaluated once)
	Body    []Stmt // optional: statements before condition (nil = simple wait)
	Tail    Expr   // optional: condition expression (nil = simple wait)
	Comment string
}

// --- Expression nodes ---

// LiteralExpr is a compile-time value: number, string, null, or resolved
// constructor literal.
type LiteralExpr struct {
	Value any // int for numbers, string for strings, false for null,
	// map[string]any for resolved constructors ({"id":"metalbar"}, etc.)
}

// IdentExpr is a variable reference or $register reference.
type IdentExpr struct {
	Name string // variable name or "$signal" etc.
}

// CallExpr is a function call used as an expression (produces a value).
type CallExpr struct {
	Name   string
	Args   []Expr
	KwArgs map[string]Expr
	Blocks []*ContinuationBlock // nil for non-branching calls
}

// InstructionExpr is an inline instruction block used as an expression.
type InstructionExpr struct {
	Frame map[string]any // includes returnSlot markers
}

// ArithExpr is a binary arithmetic expression: `a + b`
type ArithExpr struct {
	Op  tokenKind // tokPlus, tokMinus, tokStar, tokSlash
	LHS Expr
	RHS Expr
}

// CompareExpr is a comparison expression: `a > b`, `x == null`
type CompareExpr struct {
	Op  tokenKind // tokGreater, tokLess, etc.
	LHS Expr
	RHS Expr
}

// TypeCheckExpr is a type check: `x is Unit`
type TypeCheckExpr struct {
	Value    Expr
	TypeSlot string // wire-format slot key from typeCheckSlot()
}

// TruthyExpr is a bare value tested for truthiness in a boolean context.
type TruthyExpr struct {
	Value Expr
}

// BoolChainExpr is a chain of boolean sub-expressions with && or ||.
type BoolChainExpr struct {
	Op       tokenKind // tokDoubleAmpersand or tokDoublePipe
	Children []Expr    // each child is CompareExpr, TypeCheckExpr, TruthyExpr, NotExpr, or BoolChainExpr
}

// NotExpr is a negated boolean expression: `!expr`
type NotExpr struct {
	Value Expr
}

// ConstructorExpr is a type constructor: `Item("metalbar")`, `Coordinate(x, y)`
type ConstructorExpr struct {
	TypeName string // "Item", "Component", "Technology", "Value", "Coordinate"
	Args     []Expr
}

// AmpersandExpr attaches a numeric component: `Item("x") & 5`
type AmpersandExpr struct {
	Value Expr
	Num   Expr
}

// ExprListExpr is a comma-separated list of expressions: `1, my_fn args, 3`
type ExprListExpr struct {
	Exprs []Expr // each is arity 1, except CallExpr (arity = returnCount)
}

// ModeBlockExpr is a locked { ... } or unlocked { ... } used as an expression.
// The last item in the block is the value-producing tail expression.
type ModeBlockExpr struct {
	Unlock  bool   // false=locked, true=unlocked
	Body    []Stmt // leading statements (side effects, may be empty)
	Tail    Expr   // the value-producing expression
	Comment string
}

// IfExpr is an if/else-if/else expression that produces a value.
// Each branch has a body of statements and a tail expression.
// The else clause is optional; when absent, uncovered branches produce null.
type IfExpr struct {
	Cond    Expr
	Body    []Stmt
	Tail    Expr // value of the if-true branch
	ElseIfs []ElseIfExprClause
	ElsBody []Stmt
	ElsTail Expr // value of the else branch
	Comment string
}

// ElseIfExprClause is an else-if branch in an if-expression.
type ElseIfExprClause struct {
	Cond Expr
	Body []Stmt
	Tail Expr
}

// exprTailStmt is an internal wrapper for a bare expression parsed at the end
// of an expression block body. Never appears in the final AST —
// parseBhvModeBlockExpr / parseFnBodyModeBlockExpr / parseBhvIfExpr /
// parseFnBodyIfExpr extract the expr and discard the wrapper.
type exprTailStmt struct {
	Expr Expr
}

// --- Interface implementations ---

func (*CallStmt) stmtNode()           {}
func (*LetStmt) stmtNode()            {}
func (*AssignStmt) stmtNode()         {}
func (*CompoundAssignStmt) stmtNode() {}
func (*IncrDecrStmt) stmtNode()       {}
func (*MultiReturnStmt) stmtNode()    {}
func (*InstructionStmt) stmtNode()    {}
func (*ModeBlockStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()         {}
func (*IfStmt) stmtNode()             {}
func (*WhileStmt) stmtNode()          {}
func (*LoopStmt) stmtNode()           {}
func (*ForStmt) stmtNode()            {}
func (*BreakStmt) stmtNode()          {}
func (*ExitStmt) stmtNode()           {}
func (*WaitStmt) stmtNode()           {}
func (*exprTailStmt) stmtNode()       {}

func (*LiteralExpr) exprNode()      {}
func (*IdentExpr) exprNode()        {}
func (*CallExpr) exprNode()         {}
func (*InstructionExpr) exprNode()  {}
func (*ArithExpr) exprNode()        {}
func (*CompareExpr) exprNode()      {}
func (*TypeCheckExpr) exprNode()    {}
func (*TruthyExpr) exprNode()       {}
func (*BoolChainExpr) exprNode()    {}
func (*NotExpr) exprNode()          {}
func (*ConstructorExpr) exprNode()  {}
func (*AmpersandExpr) exprNode()    {}
func (*ExprListExpr) exprNode()     {}
func (*ModeBlockExpr) exprNode()    {}
func (*IfExpr) exprNode()           {}

// isTerminalStmt reports whether a statement terminates control flow
// (exit, break, or return), making any following code unreachable.
func isTerminalStmt(s Stmt) bool {
	switch s.(type) {
	case *ExitStmt, *BreakStmt, *ReturnStmt:
		return true
	}
	return false
}

// terminalKeyword returns the keyword name for a terminal statement.
func terminalKeyword(s Stmt) string {
	switch s.(type) {
	case *ExitStmt:
		return "exit"
	case *BreakStmt:
		return "break"
	case *ReturnStmt:
		return "return"
	}
	return ""
}
