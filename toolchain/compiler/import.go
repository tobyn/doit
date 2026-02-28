package compiler

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
	allAliases := map[string]int{} // alias → source position

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
			if prevPos, ok := allAliases[name.Alias]; ok {
				_ = prevPos
				return p.errorf(name.Pos, "duplicate import name %q", name.Alias)
			}
			allAliases[name.Alias] = name.Pos
		}
		if stmt.Namespace != "" {
			if prevPos, ok := allAliases[stmt.Namespace]; ok {
				_ = prevPos
				return p.errorf(stmt.Pos, "duplicate import name %q", stmt.Namespace)
			}
			allAliases[stmt.Namespace] = stmt.Pos
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

// skipImportStmt skips over an import statement during pass 2.
// The `import` keyword has already been consumed.
func (p *parser) skipImportStmt() error {
	// Skip tokens until we reach the next top-level declaration.
	// Import statements don't contain braces, so we just skip until we
	// see a token that starts a new top-level declaration or EOF.
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			p.unget(tok)
			return nil
		}
		// Import statements end when we see a top-level keyword
		if tok.kind == tokIdent {
			switch tok.val {
			case "import", "fn", "private", "behavior":
				p.unget(tok)
				return nil
			}
		}
	}
}
