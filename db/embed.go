package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed default_workspace.json
var defaultWorkspaceJSON []byte

func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic("db: migrations sub: " + err.Error())
	}
	return sub
}

func DefaultWorkspaceJSON() []byte {
	return defaultWorkspaceJSON
}
