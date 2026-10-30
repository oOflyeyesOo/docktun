package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"docktun/internal/db"
)

func relativeComposeSources(directory, encoded string) ([]string, error) {
	files := []string{}
	for _, file := range strings.Split(encoded, ",") {
		file = strings.TrimSpace(file)
		if file == "" || file == "-" {
			continue
		}
		if path.IsAbs(file) {
			var err error
			file, err = filepath.Rel(directory, file)
			if err != nil {
				return nil, err
			}
		}
		file = filepath.ToSlash(filepath.Clean(file))
		if file == "." || file == ".." || strings.HasPrefix(file, "../") || path.IsAbs(file) {
			return nil, fmt.Errorf("Compose source is outside the stack folder; mount and configure its full source before deploying")
		}
		files = append(files, file)
	}
	return files, nil
}

func rememberComposeSources(database *db.DB, id int, directory, encoded string) {
	if database == nil || id == 0 || encoded == "" || encoded == "-" {
		return
	}
	files, err := relativeComposeSources(directory, encoded)
	message := ""
	if err != nil {
		message = err.Error()
	}
	raw, _ := json.Marshal(files)
	_, _ = database.Exec(`INSERT INTO project_compose_sources(project_id,files,error) VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET files=excluded.files,error=excluded.error WHERE files<>excluded.files OR error<>excluded.error`, id, string(raw), message)
}

func storedComposeSources(database *db.DB, id int) ([]string, error) {
	var encoded, message string
	err := database.QueryRow(`SELECT files,error FROM project_compose_sources WHERE project_id=?`, id).Scan(&encoded, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if message != "" {
		return nil, errors.New(message)
	}
	var files []string
	if json.Unmarshal([]byte(encoded), &files) != nil {
		return nil, fmt.Errorf("saved Compose source list is invalid")
	}
	return files, nil
}
