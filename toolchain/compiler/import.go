package compiler

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strings"
)

// ImportStmt represents a parsed import statement.
type ImportStmt struct {
	Path      string       // "./my_library"
	Namespace string       // "lib" or "" if no namespace alias
	Names     []ImportName // named imports, nil for namespace-only
	Glob      bool         // true if * was used
	Pos       int          // for error reporting
}

// ImportName represents a single named import within an import statement.
type ImportName struct {
	Name  string // original name in source file
	Alias string // local name (== Name if no "as")
	Pos   int
}

// parseImports parses all import statements at the top of a file.
// Import statements must appear before any fn or behavior declarations.
// Stores results into p.imports.
func (p *parser) parseImports() error {
	// Track all aliases (named imports + namespace names) for collision detection
	allAliases := map[string]bool{}

	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			p.unget(tok)
			return nil
		}
		if tok.kind != tokIdent || tok.val != "import" {
			p.unget(tok)
			return nil
		}

		stmt, err := p.parseImportStmt()
		if err != nil {
			return err
		}

		// Check for duplicate aliases within and across statements
		for _, name := range stmt.Names {
			if allAliases[name.Alias] {
				return p.errorf(name.Pos, "duplicate import name %q", name.Alias)
			}
			allAliases[name.Alias] = true
		}
		if stmt.Namespace != "" {
			if allAliases[stmt.Namespace] {
				return p.errorf(stmt.Pos, "duplicate import name %q", stmt.Namespace)
			}
			allAliases[stmt.Namespace] = true
		}

		p.imports = append(p.imports, stmt)
	}
}

// parseImportStmt parses a single import statement after the `import` keyword.
// Syntax forms:
//
//	import <names> from "<path>"
//	import "<path>" as <namespace>
//	import <names> from "<path>" as <namespace>
func (p *parser) parseImportStmt() (ImportStmt, error) {
	startPos := p.pos

	// Peek to determine form: path string or name list
	tok, err := p.next()
	if err != nil {
		return ImportStmt{}, err
	}

	if tok.kind == tokString {
		// Namespace-only: import "<path>" as <namespace>
		path := tok.val
		if err := validateImportPath(p, path, tok.pos); err != nil {
			return ImportStmt{}, err
		}

		asTok, err := p.expect(tokIdent)
		if err != nil {
			return ImportStmt{}, err
		}
		if asTok.val != "as" {
			return ImportStmt{}, p.errorf(asTok.pos, "expected 'as' after import path, got %s", asTok.describe())
		}

		nsTok, err := p.expect(tokIdent)
		if err != nil {
			return ImportStmt{}, err
		}
		if err := validateImportAlias(p, nsTok); err != nil {
			return ImportStmt{}, err
		}

		return ImportStmt{
			Path:      path,
			Namespace: nsTok.val,
			Pos:       startPos,
		}, nil
	}

	// Named import form: import <names> from "<path>" [as <namespace>]
	p.unget(tok)
	names, hasGlob, err := p.parseImportNames()
	if err != nil {
		return ImportStmt{}, err
	}

	// Expect "from"
	fromTok, err := p.expect(tokIdent)
	if err != nil {
		return ImportStmt{}, err
	}
	if fromTok.val != "from" {
		return ImportStmt{}, p.errorf(fromTok.pos, "expected 'from' after import names, got %s", fromTok.describe())
	}

	// Expect path string
	pathTok, err := p.expect(tokString)
	if err != nil {
		return ImportStmt{}, err
	}
	if err := validateImportPath(p, pathTok.val, pathTok.pos); err != nil {
		return ImportStmt{}, err
	}

	stmt := ImportStmt{
		Path:  pathTok.val,
		Names: names,
		Glob:  hasGlob,
		Pos:   startPos,
	}

	// Optional namespace: "as <namespace>"
	peek, err := p.next()
	if err != nil {
		return ImportStmt{}, err
	}
	if peek.kind == tokIdent && peek.val == "as" {
		nsTok, err := p.expect(tokIdent)
		if err != nil {
			return ImportStmt{}, err
		}
		if err := validateImportAlias(p, nsTok); err != nil {
			return ImportStmt{}, err
		}
		stmt.Namespace = nsTok.val
	} else {
		p.unget(peek)
	}

	return stmt, nil
}

// parseImportNames parses the name list in an import statement.
// Returns the list of named imports and whether a glob (*) was present.
func (p *parser) parseImportNames() ([]ImportName, bool, error) {
	var names []ImportName
	hasGlob := false
	localNames := map[string]bool{} // per-statement duplicate tracking

	for {
		tok, err := p.next()
		if err != nil {
			return nil, false, err
		}

		if tok.kind == tokStar {
			if hasGlob {
				return nil, false, p.errorf(tok.pos, "duplicate '*' in import statement")
			}
			hasGlob = true
		} else if tok.kind == tokIdent {
			if tok.val == "from" {
				// We've hit the 'from' keyword — unget and return
				p.unget(tok)
				break
			}
			name := ImportName{Name: tok.val, Alias: tok.val, Pos: tok.pos}

			// Check for "as <alias>"
			peek, err := p.next()
			if err != nil {
				return nil, false, err
			}
			if peek.kind == tokIdent && peek.val == "as" {
				aliasTok, err := p.expect(tokIdent)
				if err != nil {
					return nil, false, err
				}
				if err := validateImportAlias(p, aliasTok); err != nil {
					return nil, false, err
				}
				name.Alias = aliasTok.val
			} else {
				p.unget(peek)
				// Validate the original name as an alias (it will be used as the local name)
				if Keywords[tok.val] {
					return nil, false, p.errorf(tok.pos, "%q is a reserved keyword and cannot be used as an import name", tok.val)
				}
			}

			if localNames[name.Alias] {
				return nil, false, p.errorf(name.Pos, "duplicate import name %q in statement", name.Alias)
			}
			localNames[name.Alias] = true
			names = append(names, name)
		} else {
			return nil, false, p.errorf(tok.pos, "expected import name or '*', got %s", tok.describe())
		}

		// Check for comma separator
		sep, err := p.next()
		if err != nil {
			return nil, false, err
		}
		if sep.kind != tokComma {
			p.unget(sep)
			break
		}
	}

	if len(names) == 0 && !hasGlob {
		return nil, false, p.errorf(p.pos, "expected at least one import name or '*'")
	}

	return names, hasGlob, nil
}

// validateImportPath checks that an import path starts with "./" or "../" or "std:".
func validateImportPath(p *parser, path string, pos int) error {
	if path == "" {
		return p.errorf(pos, "import path cannot be empty")
	}
	hasRelative := len(path) >= 2 && path[:2] == "./"
	hasParent := len(path) >= 3 && path[:3] == "../"
	hasStdlib := len(path) >= 4 && path[:4] == "std:"
	if hasRelative || hasParent || hasStdlib {
		return nil
	}
	return p.errorf(pos, "import path must start with \"./\", \"../\", or \"std:\"")
}

// validateImportAlias checks that an alias is a valid non-keyword identifier.
func validateImportAlias(p *parser, tok token) error {
	if Keywords[tok.val] {
		return p.errorf(tok.pos, "%q is a reserved keyword and cannot be used as an import alias", tok.val)
	}
	return nil
}

// skipToNextDecl skips tokens until the next top-level declaration keyword
// (import, fn, private, behavior, const) or EOF. Used during pass 2 to skip
// over import and const declarations whose keywords have already been consumed.
func (p *parser) skipToNextDecl() error {
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			p.unget(tok)
			return nil
		}
		if tok.kind == tokIdent {
			switch tok.val {
			case "import", "fn", "private", "behavior", "const":
				p.unget(tok)
				return nil
			}
		}
	}
}

// resolveImportPath resolves an import path to a file path and file system.
// Returns the resolved file path (with .doit extension) and the fs.FS to read from.
func (p *parser) resolveImportPath(importPath string, pos int) (string, fs.FS, error) {
	if strings.HasPrefix(importPath, "std:") {
		// Standard library import
		if p.stdlibFS == nil {
			return "", nil, p.errorf(pos, "stdlib not available for import")
		}
		relPath := importPath[4:] + ".doit"
		return relPath, p.stdlibFS, nil
	}

	// Relative import (./ or ../)
	if p.sourceFS == nil {
		return "", nil, p.errorf(pos, "imports require a source file path; pass a file argument instead of stdin")
	}

	resolved := path.Join(p.sourceDir, importPath+".doit")
	// Clean the path to normalize ../ traversals
	resolved = path.Clean(resolved)

	// Self-import check
	if resolved == p.sourcePath {
		return "", nil, p.errorf(pos, "file cannot import itself")
	}

	return resolved, p.sourceFS, nil
}

// importedFile holds the parsed results from an imported file.
type importedFile struct {
	fns    map[string]*fnDef
	consts map[string]*constDef
}

// parseImportedFile reads and parses a file, returning its exported function and constant definitions.
// The returned maps include private entries (with private=true) for error reporting.
func (p *parser) parseImportedFile(fsys fs.FS, filePath string, pos int) (*importedFile, error) {
	// Circular import check
	for _, stackPath := range p.importStack {
		if stackPath == filePath {
			cycle := append(p.importStack, filePath)
			return nil, p.errorf(pos, "circular import: %s", strings.Join(cycle, " → "))
		}
	}

	data, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		return nil, p.errorf(pos, "cannot read import %q: %v", filePath, err)
	}

	// Clone the cached stdlib fns for this imported file's parser.
	stdlibFns := maps.Clone(p.stdlibFns)

	sourceDir := path.Dir(filePath)
	if sourceDir == "." {
		sourceDir = ""
	}

	// Create a parser for the imported file
	ip := &parser{
		scanner: scanner{
			src:        string(data),
			locale:     p.locale,
			sourceFile: filePath,
		},
		fns:         stdlibFns,
		consts:      map[string]*constDef{},
		loopLabels:  map[string]bool{},
		sourceFS:    p.sourceFS,
		sourcePath:  filePath,
		sourceDir:   sourceDir,
		stdlibFS:    p.stdlibFS,
		stdlibFns:   p.stdlibFns,
		importStack: append(append([]string{}, p.importStack...), filePath),
	}

	// Parse imports recursively, then collect function and constant definitions
	if err := ip.parseImports(); err != nil {
		return nil, err
	}

	if err := ip.processImports(); err != nil {
		return nil, err
	}

	// Collect user-defined functions and constants (pass 1 — skips behaviors)
	if err := ip.collectImportedFns(); err != nil {
		return nil, err
	}

	// Build scope: all non-stdlib functions available in this file.
	// Functions with bodies need this scope so that transitive dependencies
	// (functions they call but the importer didn't explicitly import) are
	// available during expandCall inlining.
	scope := map[string]*fnDef{}
	for name, fn := range ip.fns {
		if p.stdlibFns[name] == nil {
			scope[name] = fn
		}
	}

	// Extract only user-defined functions (exclude stdlib)
	resultFns := map[string]*fnDef{}
	for name, fn := range scope {
		if fn.astBody != nil && fn.scope == nil {
			fn.scope = scope
		}
		resultFns[name] = fn
	}

	return &importedFile{fns: resultFns, consts: ip.consts}, nil
}

// collectImportedFns collects function and constant definitions from an imported file.
func (p *parser) collectImportedFns() error {
	return p.collectDecls(true)
}

// processImports resolves all parsed import statements and merges imported
// functions and constants into the parser's tables.
func (p *parser) processImports() error {
	if len(p.imports) == 0 {
		return nil
	}

	// Cache of already-parsed files to avoid re-parsing the same file
	fileCache := map[string]*importedFile{}

	// Track named imports and namespace names for post-collectUserFns collision checking
	p.namedImports = map[string]bool{}
	p.namespaceNames = map[string]bool{}

	for _, stmt := range p.imports {
		filePath, fsys, err := p.resolveImportPath(stmt.Path, stmt.Pos)
		if err != nil {
			return err
		}

		// Parse the file (use cache if already parsed)
		imported, ok := fileCache[filePath]
		if !ok {
			imported, err = p.parseImportedFile(fsys, filePath, stmt.Pos)
			if err != nil {
				return err
			}
			fileCache[filePath] = imported
		}

		fns := imported.fns
		consts := imported.consts

		// Process glob imports — add all non-private functions and constants.
		// Globs are last-wins: later glob imports shadow earlier ones.
		if stmt.Glob {
			for name, fn := range fns {
				if !fn.private {
					p.fns[name] = fn
				}
			}
			for name, c := range consts {
				if !c.private {
					p.consts[name] = c
				}
			}
		}

		// Process named imports
		for _, imp := range stmt.Names {
			fn, fnExists := fns[imp.Name]
			c, cExists := consts[imp.Name]
			if !fnExists && !cExists {
				return p.errorf(imp.Pos, "%q not found in %q", imp.Name, stmt.Path)
			}
			if fnExists {
				if fn.private {
					return p.errorf(imp.Pos, "cannot import private function %q from %q", imp.Name, stmt.Path)
				}
				p.fns[imp.Alias] = fn
			}
			if cExists {
				if c.private {
					return p.errorf(imp.Pos, "cannot import private constant %q from %q", imp.Name, stmt.Path)
				}
				p.consts[imp.Alias] = c
			}
			p.namedImports[imp.Alias] = true
			// When a glob and a rename coexist in the same statement,
			// the rename replaces the original name — remove the
			// glob-imported original so only the alias is accessible.
			if stmt.Glob && imp.Alias != imp.Name {
				delete(p.fns, imp.Name)
				delete(p.consts, imp.Name)
			}
		}

		// Process namespace imports
		if stmt.Namespace != "" {
			nsFns := maps.Clone(fns)
			if p.namespaces == nil {
				p.namespaces = map[string]map[string]*fnDef{}
			}
			p.namespaces[stmt.Namespace] = nsFns
			p.namespaceNames[stmt.Namespace] = true

			nsConsts := maps.Clone(consts)
			if p.namespaceConsts == nil {
				p.namespaceConsts = map[string]map[string]*constDef{}
			}
			p.namespaceConsts[stmt.Namespace] = nsConsts
		}
	}

	return nil
}

// checkImportCollisions checks for collisions between same-file function/constant
// definitions and named imports or namespace names. Called after collectUserFns.
func (p *parser) checkImportCollisions(fnNames []string, constNames []string) error {
	for _, name := range fnNames {
		if p.namedImports[name] {
			return fmt.Errorf("function %q conflicts with a named import", name)
		}
		if p.namespaceNames[name] {
			return fmt.Errorf("function %q conflicts with an import namespace", name)
		}
	}
	for _, name := range constNames {
		if p.namedImports[name] {
			return fmt.Errorf("constant %q conflicts with a named import", name)
		}
		if p.namespaceNames[name] {
			return fmt.Errorf("constant %q conflicts with an import namespace", name)
		}
	}
	return nil
}

// resolveFnName resolves a potential qualified function name (ns.fn or ns.CONST).
// If the token is a namespace name, peeks for a dot and resolves the qualified
// name. Returns the effective name, the fnDef, and any error.
// A nil fnDef with no error means the name was not found as a function
// (it might be a constant — the caller should check p.consts).
func (p *parser) resolveFnName(tok token) (string, *fnDef, error) {
	_, isNs := p.namespaces[tok.val]
	if !isNs {
		_, isNs = p.namespaceConsts[tok.val]
	}
	if isNs {
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
			// Check functions first
			if nsFns, ok := p.namespaces[tok.val]; ok {
				if fn, exists := nsFns[memberTok.val]; exists {
					if fn.private {
						return "", nil, p.errorf(memberTok.pos, "cannot access private function %q in namespace %q", memberTok.val, tok.val)
					}
					p.fns[qualName] = fn
					return qualName, fn, nil
				}
			}
			// Then check constants
			if nsConsts, ok := p.namespaceConsts[tok.val]; ok {
				if c, exists := nsConsts[memberTok.val]; exists {
					if c.private {
						return "", nil, p.errorf(memberTok.pos, "cannot access private constant %q in namespace %q", memberTok.val, tok.val)
					}
					p.consts[qualName] = c
					return qualName, nil, nil
				}
			}
			return "", nil, p.errorf(memberTok.pos, "%q not found in namespace %q", memberTok.val, tok.val)
		}
		p.unget(peek)
	}
	return tok.val, p.fns[tok.val], nil
}
