package localast

import (
	"github.com/hashicorp/sentinel/lang/ast"
	"github.com/hashicorp/sentinel/lang/semantic"
	"github.com/hashicorp/sentinel/lang/token"
)

// Compiled is a "compiled" Sentinel file/fileset grouping. This is a processed
// policy or module that contains various AST modifications and preprocessing
// and is ready for execution.
//
// A compiled policy can be evaluated concurrently.
type Compiled struct {
	file    *ast.File      // File to execute
	fileSet *token.FileSet // FileSet for positional information
}

// CompileOpts are options for compilation.
type CompileOpts struct {
	File         *ast.File          // File to execute
	FileSet      *token.FileSet     // FileSet for positional information
	SkipCheckers []semantic.Checker // List of semantic checks to skip
}

// Compile compiles the given policy file.
//
// Because evaluation is done via an interpreter, "compile" means to rewrite
// the AST in some forms and to precompute some values. It results in a
// more efficient execution for the interpreter.
//
// Once a policy has been compiled, the AST must not be reused. It will be
// modified in-place.
func Compile(opts *CompileOpts) (*Compiled, error) {
	// Verify semantics
	if err := semantic.Check(semantic.CheckOpts{
		File:         opts.File,
		FileSet:      opts.FileSet,
		SkipCheckers: opts.SkipCheckers,
	}); err != nil {
		return nil, err
	}

	// Rewrite the import expressions
	file := ast.Rewrite(opts.File, rewriteImportSelector()).(*ast.File)

	// Build
	return &Compiled{
		file:    file,
		fileSet: opts.FileSet,
	}, nil
}

// File returns the file portion of the compiled data.
func (c *Compiled) File() *ast.File {
	return c.file
}

// FileSet returns the file set portion of the compiled data.
func (c *Compiled) FileSet() *token.FileSet {
	return c.fileSet
}