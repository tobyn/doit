package compiler

import (
	"io"

	"github.com/tobyn/doit/toolchain/codec"
)

// Compile reads doit source from r and compiles it into a codec Object.
func Compile(r io.Reader) (*codec.Object, error) {
	_, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

// CompileString compiles doit source into a codec Object.
func CompileString(src string) (*codec.Object, error) {
	return nil, nil
}
