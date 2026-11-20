package service

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"docktun/internal/db"
)

var (
	ErrRevisionNotFound = errors.New("compose revision not found")
)

type ComposeRevision struct {
	ID        int    `json:"id"`
	ProjectID int    `json:"project_id"`
	Filename  string `json:"filename"`
	Content   string `json:"content"`
	Message   string `json:"message"`
	UserID    int    `json:"user_id"`
	CreatedAt string `json:"created_at"`
	Size      int    `json:"size"`
}

// RecordComposeRevision records a new versioned revision into compose_revisions.
// If the content is identical to the most recent revision for this project and file,
// it avoids creating a redundant identical revision and returns the existing one.
func RecordComposeRevision(database *db.DB, projectID int, filename, content, message string, userID int) (*ComposeRevision, error) {
	if database == nil {
		return nil, errors.New("database connection is nil")
	}
	if filename == "" {
		filename = "docker-compose.yml"
	}
	content = strings.TrimSpace(content)

	// Check latest revision to prevent duplicate identical entries
	var latestID, latestUser int
	var latestFile, latestContent, latestMsg, latestCreated string
	err := database.QueryRow(
		`SELECT id, project_id, filename, content, message, user_id, created_at 
		 FROM compose_revisions 
		 WHERE project_id = ? AND filename = ? 
		 ORDER BY id DESC LIMIT 1`,
		projectID, filename,
	).Scan(&latestID, &projectID, &latestFile, &latestContent, &latestMsg, &latestUser, &latestCreated)

	if err == nil && latestContent == content {
		return &ComposeRevision{
			ID:        latestID,
			ProjectID: projectID,
			Filename:  latestFile,
			Content:   latestContent,
			Message:   latestMsg,
			UserID:    latestUser,
			CreatedAt: latestCreated,
			Size:      len(latestContent),
		}, nil
	}

	if message == "" {
		if err == sql.ErrNoRows {
			message = "Initial revision"
		} else {
			message = "Updated compose file"
		}
	}

	res, err := database.Exec(
		`INSERT INTO compose_revisions (project_id, filename, content, message, user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		projectID, filename, content, message, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("insert compose revision: %w", err)
	}

	insertedID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get last insert id: %w", err)
	}

	var rev ComposeRevision
	err = database.QueryRow(
		`SELECT id, project_id, filename, content, message, user_id, created_at 
		 FROM compose_revisions 
		 WHERE id = ?`,
		insertedID,
	).Scan(&rev.ID, &rev.ProjectID, &rev.Filename, &rev.Content, &rev.Message, &rev.UserID, &rev.CreatedAt)
	if err != nil {
		// Fallback if query row fails
		rev = ComposeRevision{
			ID:        int(insertedID),
			ProjectID: projectID,
			Filename:  filename,
			Content:   content,
			Message:   message,
			UserID:    userID,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Size:      len(content),
		}
	} else {
		rev.Size = len(rev.Content)
	}

	return &rev, nil
}

// ListComposeRevisions returns all revisions for a project ordered by id DESC.
func ListComposeRevisions(database *db.DB, projectID int) ([]ComposeRevision, error) {
	if database == nil {
		return nil, errors.New("database connection is nil")
	}

	rows, err := database.Query(
		`SELECT id, project_id, filename, content, message, user_id, created_at 
		 FROM compose_revisions 
		 WHERE project_id = ? 
		 ORDER BY id DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list compose revisions: %w", err)
	}
	defer rows.Close()

	revisions := []ComposeRevision{}
	for rows.Next() {
		var rev ComposeRevision
		if err := rows.Scan(&rev.ID, &rev.ProjectID, &rev.Filename, &rev.Content, &rev.Message, &rev.UserID, &rev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan compose revision: %w", err)
		}
		rev.Size = len(rev.Content)
		revisions = append(revisions, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return revisions, nil
}

// GetComposeRevision returns a single revision by project ID and revision ID.
func GetComposeRevision(database *db.DB, projectID int, revID int) (*ComposeRevision, error) {
	if database == nil {
		return nil, errors.New("database connection is nil")
	}

	var rev ComposeRevision
	err := database.QueryRow(
		`SELECT id, project_id, filename, content, message, user_id, created_at 
		 FROM compose_revisions 
		 WHERE project_id = ? AND id = ?`,
		projectID, revID,
	).Scan(&rev.ID, &rev.ProjectID, &rev.Filename, &rev.Content, &rev.Message, &rev.UserID, &rev.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRevisionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get compose revision: %w", err)
	}
	rev.Size = len(rev.Content)
	return &rev, nil
}

// SnapshotExistingComposeIfEmpty records the current on-disk compose file as Revision 1
// only if an actual compose file physically exists on disk and no revisions exist yet.
func SnapshotExistingComposeIfEmpty(database *db.DB, projects *Projects, projectID int) {
	if database == nil || projects == nil {
		return
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM compose_revisions WHERE project_id = ?`, projectID).Scan(&count); err != nil || count > 0 {
		return
	}
	p, err := projects.GetByID(projectID)
	if err != nil {
		return
	}
	candidates := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for _, c := range candidates {
		fullPath := filepath.Join(p.Path, c)
		if fi, err := os.Stat(fullPath); err == nil && fi.Mode().IsRegular() {
			data, err := os.ReadFile(fullPath)
			if err == nil && len(strings.TrimSpace(string(data))) > 0 {
				_, _ = RecordComposeRevision(database, projectID, c, string(data), "Base snapshot before initial edit", 0)
			}
			return
		}
	}
}

// RollbackComposeRevision restores the compose file to the specified revision,
// saving the file and creating a new revision entry recording the rollback.
func RollbackComposeRevision(projects *Projects, projectID int, revID int, userID int) (*ComposeRevision, error) {
	if projects == nil {
		return nil, errors.New("projects service is nil")
	}

	targetRev, err := GetComposeRevision(projects.db, projectID, revID)
	if err != nil {
		return nil, err
	}

	// Save target content to project file
	if err := projects.SaveComposeFile(projectID, targetRev.Filename, targetRev.Content); err != nil {
		return nil, fmt.Errorf("write compose file during rollback: %w", err)
	}

	// Record explicit rollback revision entry
	rollbackMsg := fmt.Sprintf("Rollback to revision #%d (%s)", targetRev.ID, targetRev.CreatedAt)
	newRev, err := RecordComposeRevision(projects.db, projectID, targetRev.Filename, targetRev.Content, rollbackMsg, userID)
	if err != nil {
		// If duplicate check returned existing, still return success
		return targetRev, nil
	}
	return newRev, nil
}
