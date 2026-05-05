package handlers

import "database/sql"

// errSQLNoRows is exported as a package-level alias so handlers can compare
// without importing database/sql purely for the sentinel.
var errSQLNoRows = sql.ErrNoRows
