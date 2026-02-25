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

// --- Statement nodes ---

// CallStmt is a bare function call statement: `notify "Hello"`
type CallStmt struct {
	Name    string
	Args    []Expr
	KwArgs  map[string]Expr
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
	Target  string // variable name or $register
	Value   Expr
	Comment string
}

// CompoundAssignStmt is a compound assignment: `x += 3`
type CompoundAssignStmt struct {
	Target  string
	Op      tokenKind // tokPlusEquals, tokMinusEquals, etc.
	Value   Expr
	Comment string
}

// IncrDecrStmt is an increment or decrement: `x++`, `x--`
type IncrDecrStmt struct {
	Target  string
	Op      tokenKind // tokPlusPlus or tokMinusMinus
	Comment string
}

// MultiReturnStmt is a multi-return binding: `let x, y = fn args`
type MultiReturnStmt struct {
	Bindings []MultiBinding
	Value    Expr // CallExpr or InstructionExpr
	Comment  string
}

// MultiBinding is a single binding in a multi-return statement.
type MultiBinding struct {
	Name    string // "" for discard
	Discard bool   // true for _
	Mutable bool   // true for var, false for let (only meaningful when !Discard)
}

// InstructionStmt is a bare instruction block: `instruction "op" { ... }`
type InstructionStmt struct {
	Frame   map[string]any
	Comment string
}

// LockStmt is a lock or unlock statement.
type LockStmt struct {
	Unlock  bool // false=lock, true=unlock
	Comment string
}

// ReturnStmt is a return from a function body: `return x, y`
type ReturnStmt struct {
	Values []Expr // nil for bare return (not currently used)
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
	Cond    Expr
	Body    []Stmt
	Comment string
}

// LoopStmt is an unconditional loop.
type LoopStmt struct {
	Body    []Stmt
	Comment string
}

// BreakStmt is a break from a loop.
type BreakStmt struct {
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
	Children []Expr    // each child is CompareExpr, TypeCheckExpr, TruthyExpr, or BoolChainExpr
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

// --- Interface implementations ---

func (*CallStmt) stmtNode()           {}
func (*LetStmt) stmtNode()            {}
func (*AssignStmt) stmtNode()         {}
func (*CompoundAssignStmt) stmtNode() {}
func (*IncrDecrStmt) stmtNode()       {}
func (*MultiReturnStmt) stmtNode()    {}
func (*InstructionStmt) stmtNode()    {}
func (*LockStmt) stmtNode()           {}
func (*ReturnStmt) stmtNode()         {}
func (*IfStmt) stmtNode()             {}
func (*WhileStmt) stmtNode()          {}
func (*LoopStmt) stmtNode()           {}
func (*BreakStmt) stmtNode()          {}

func (*LiteralExpr) exprNode()      {}
func (*IdentExpr) exprNode()        {}
func (*CallExpr) exprNode()         {}
func (*InstructionExpr) exprNode()  {}
func (*ArithExpr) exprNode()        {}
func (*CompareExpr) exprNode()      {}
func (*TypeCheckExpr) exprNode()    {}
func (*TruthyExpr) exprNode()       {}
func (*BoolChainExpr) exprNode()    {}
func (*ConstructorExpr) exprNode()  {}
func (*AmpersandExpr) exprNode()    {}
