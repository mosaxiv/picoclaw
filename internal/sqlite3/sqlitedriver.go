package sqlite3

import (
	_ "embed"

	nsqlite3 "github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver"
)

//go:embed sqlite-vec.wasm
var sqliteVecWASM []byte

func init() {
	nsqlite3.Binary = sqliteVecWASM
}
